package user

import (
	"context"
	"github.com/vardius/go-api-boilerplate/pkg/domain"
	"log"

	"github.com/google/uuid"
)

// WasRegisteredWithFacebook event
type WasRegisteredWithFacebook struct {
	ID        uuid.UUID `json:"id"`
	Email     string    `json:"email"`
	AuthToken string    `json:"authToken"`
}

func onWasRegisteredWithFacebook(ctx context.Context, event domain.Event) {
	// todo: register user
	log.Printf("handle %v", event)
}
