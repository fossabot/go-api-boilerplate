package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	_ "github.com/go-sql-driver/mysql"
	http_cors "github.com/rs/cors"
	auth_proto "github.com/vardius/go-api-boilerplate/cmd/auth/infrastructure/proto"
	user_config "github.com/vardius/go-api-boilerplate/cmd/user/application/config"
	user_eventhandler "github.com/vardius/go-api-boilerplate/cmd/user/application/eventhandler"
	user_security "github.com/vardius/go-api-boilerplate/cmd/user/application/security"
	"github.com/vardius/go-api-boilerplate/cmd/user/domain/user"
	user_persistence "github.com/vardius/go-api-boilerplate/cmd/user/infrastructure/persistence/mysql"
	user_proto "github.com/vardius/go-api-boilerplate/cmd/user/infrastructure/proto"
	user_repository "github.com/vardius/go-api-boilerplate/cmd/user/infrastructure/repository"
	user_grpc "github.com/vardius/go-api-boilerplate/cmd/user/interfaces/grpc"
	user_http "github.com/vardius/go-api-boilerplate/cmd/user/interfaces/http"
	commandbus "github.com/vardius/go-api-boilerplate/pkg/commandbus"
	eventbus "github.com/vardius/go-api-boilerplate/pkg/eventbus"
	eventstore "github.com/vardius/go-api-boilerplate/pkg/eventstore/memory"
	grpc_utils "github.com/vardius/go-api-boilerplate/pkg/grpc"
	http_recovery "github.com/vardius/go-api-boilerplate/pkg/http/recovery"
	http_response "github.com/vardius/go-api-boilerplate/pkg/http/response"
	http_authenticator "github.com/vardius/go-api-boilerplate/pkg/http/security/authenticator"
	"github.com/vardius/go-api-boilerplate/pkg/log"
	"github.com/vardius/go-api-boilerplate/pkg/mysql"
	gorouter "github.com/vardius/gorouter/v4"
	pubsub_proto "github.com/vardius/pubsub/proto"
	"github.com/vardius/shutdown"
	"golang.org/x/oauth2"
	"google.golang.org/grpc"
	grpc_health "google.golang.org/grpc/health"
	grpc_health_proto "google.golang.org/grpc/health/grpc_health_v1"
)

func main() {
	ctx := context.Background()
	logger := log.New(user_config.Env.Environment)
	grpcServer := grpc_utils.NewServer(user_config.Env, logger)

	db := mysql.NewConnection(ctx, user_config.Env, logger)
	defer db.Close()

	oauth2Config := oauth2.Config{
		ClientID:     user_config.Env.ClientID,
		ClientSecret: user_config.Env.ClientSecret,
		Scopes:       []string{"all"},
		Endpoint: oauth2.Endpoint{
			AuthURL:  fmt.Sprintf("http://%s:%d/authorize", user_config.Env.AuthHost, user_config.Env.PortHTTP),
			TokenURL: fmt.Sprintf("http://%s:%d/token", user_config.Env.AuthHost, user_config.Env.PortHTTP),
		},
	}

	pubsubConn := grpc_utils.NewConnection(ctx, user_config.Env.PubSubHost, user_config.Env.PortGRPC, user_config.Env, logger)
	defer pubsubConn.Close()

	grpPubSubClient := pubsub_proto.NewMessageBusClient(pubsubConn)

	eventStore := eventstore.New()
	commandBus := commandbus.New(user_config.Env.CommandBusQueueSize, logger)
	eventBus := eventbus.New(grpPubSubClient, logger)

	userRepository := user_repository.NewUserRepository(eventStore, eventBus)
	userMYSQLRepository := user_persistence.NewUserRepository(db)

	userServer := user_grpc.NewServer(commandBus, userMYSQLRepository, logger)

	authConn := grpc_utils.NewConnection(ctx, user_config.Env.AuthHost, user_config.Env.PortGRPC, user_config.Env, logger)
	defer authConn.Close()

	userConn := grpc_utils.NewConnection(ctx, user_config.Env.Host, user_config.Env.PortGRPC, user_config.Env, logger)
	defer userConn.Close()

	grpAuthClient := auth_proto.NewAuthenticationServiceClient(authConn)

	healthServer := grpc_health.NewServer()
	healthServer.SetServingStatus("user", grpc_health_proto.HealthCheckResponse_SERVING)

	auth := http_authenticator.NewToken(user_security.TokenAuthHandler(grpAuthClient, user_persistence.NewUserRepository(db)))

	http_recovery.WithLogger(logger)
	http_response.WithLogger(logger)

	// Global middleware
	router := gorouter.New(
		logger.LogRequest,
		http_cors.Default().Handler,
		http_response.WithXSS,
		http_response.WithHSTS,
		http_response.AsJSON,
		auth.FromHeader("USER"),
		auth.FromQuery("authToken"),
		http_recovery.WithRecover,
	)

	user_proto.RegisterUserServiceServer(grpcServer, userServer)
	grpc_health_proto.RegisterHealthServer(grpcServer, healthServer)

	user_http.AddHealthCheckRoutes(router, db, map[string]*grpc.ClientConn{
		"user":   userConn,
		"auth":   authConn,
		"pubsub": pubsubConn,
	})
	user_http.AddAuthRoutes(router, commandBus, oauth2Config, user_config.Env.Secret)
	user_http.AddUserRoutes(router, commandBus, userMYSQLRepository)

	srv := &http.Server{
		Addr:         fmt.Sprintf("%s:%d", user_config.Env.Host, user_config.Env.PortHTTP),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
		Handler:      router,
	}

	lis, err := net.Listen("tcp", fmt.Sprintf("%s:%d", user_config.Env.Host, user_config.Env.PortGRPC))
	if err != nil {
		logger.Critical(ctx, "tcp failed to listen %s:%d\n%v\n", user_config.Env.Host, user_config.Env.PortGRPC, err)
		os.Exit(1)
	}

	commandBus.Subscribe((user.RegisterWithEmail{}).GetName(), user.OnRegisterWithEmail(userRepository, db))
	commandBus.Subscribe((user.RegisterWithGoogle{}).GetName(), user.OnRegisterWithGoogle(userRepository, db))
	commandBus.Subscribe((user.RegisterWithFacebook{}).GetName(), user.OnRegisterWithFacebook(userRepository, db))
	commandBus.Subscribe((user.ChangeEmailAddress{}).GetName(), user.OnChangeEmailAddress(userRepository, db))
	commandBus.Subscribe((user.RequestAccessToken{}).GetName(), user.OnRequestAccessToken(userRepository, db))

	go func() {
		user_eventhandler.Register(
			pubsubConn,
			eventBus,
			map[string]eventbus.EventHandler{
				(user.WasRegisteredWithEmail{}).GetType():    user_eventhandler.WhenUserWasRegisteredWithEmail(db, userMYSQLRepository),
				(user.WasRegisteredWithGoogle{}).GetType():   user_eventhandler.WhenUserWasRegisteredWithGoogle(db, userMYSQLRepository),
				(user.WasRegisteredWithFacebook{}).GetType(): user_eventhandler.WhenUserWasRegisteredWithFacebook(db, userMYSQLRepository),
				(user.EmailAddressWasChanged{}).GetType():    user_eventhandler.WhenUserEmailAddressWasChanged(db, userMYSQLRepository),
				(user.AccessTokenWasRequested{}).GetType():   user_eventhandler.WhenUserAccessTokenWasRequested(oauth2Config, user_config.Env.Secret),
			},
			5*time.Minute,
		)
	}()

	stop := func() {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		logger.Info(ctx, "shutting down...\n")

		grpcServer.GracefulStop()

		if err := srv.Shutdown(ctx); err != nil {
			logger.Critical(ctx, "shutdown error: %v\n", err)
		} else {
			logger.Info(ctx, "gracefully stopped\n")
		}
	}

	go func() {
		logger.Critical(ctx, "failed to serve: %v\n", grpcServer.Serve(lis))
		stop()
		os.Exit(1)
	}()

	go func() {
		logger.Critical(ctx, "%v\n", srv.ListenAndServe())
		stop()
		os.Exit(1)
	}()

	logger.Info(ctx, "tcp running at %s:%d\n", user_config.Env.Host, user_config.Env.PortGRPC)
	logger.Info(ctx, "http running at %s:%d\n", user_config.Env.Host, user_config.Env.PortHTTP)

	shutdown.GracefulStop(stop)
}
