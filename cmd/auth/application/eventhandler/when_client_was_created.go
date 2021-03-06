package eventhandler

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"

	"github.com/vardius/go-api-boilerplate/cmd/auth/domain/client"
	"github.com/vardius/go-api-boilerplate/cmd/auth/infrastructure/persistence"
	"github.com/vardius/go-api-boilerplate/pkg/domain"
	"github.com/vardius/go-api-boilerplate/pkg/eventbus"
)

// WhenClientWasCreated handles event
func WhenClientWasCreated(db *sql.DB, repository persistence.ClientRepository) eventbus.EventHandler {
	fn := func(ctx context.Context, event domain.Event) {
		// this goroutine runs independently to request's goroutine,
		// there for recover middlewears will not recover from panic to prevent crash
		defer recoverEventHandler()

		log.Printf("[EventHandler] %s", event.Payload)

		e := client.WasCreated{}

		err := json.Unmarshal(event.Payload, &e)
		if err != nil {
			log.Printf("[EventHandler] Error: %v", err)
			return
		}

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			log.Printf("[EventHandler] Error: %v", err)
			return
		}
		defer tx.Rollback()

		err = repository.Add(ctx, clientModel{e})
		if err != nil {
			log.Printf("[EventHandler] Error: %v", err)
			return
		}

		tx.Commit()
	}

	return eventbus.EventHandler(fn)
}

type clientModel struct {
	e client.WasCreated
}

// GetID client id
func (c clientModel) GetID() string {
	return c.e.ID.String()
}

// GetSecret client domain
func (c clientModel) GetSecret() string {
	return c.e.Secret
}

// GetDomain client domain
func (c clientModel) GetDomain() string {
	return c.e.Domain
}

// GetUserID user id
func (c clientModel) GetUserID() string {
	return c.e.UserID.String()
}

// GetData client data
func (c clientModel) GetData() json.RawMessage {
	return c.e.Data
}
