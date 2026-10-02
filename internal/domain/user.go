package domain

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// User is an account that can sign in. Username is the unique login identifier;
// HashedPassword is a bcrypt hash and is never serialized to JSON.
type User struct {
	ID             primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Username       string             `bson:"username" json:"username"`
	HashedPassword string             `bson:"hashed_password" json:"-"`
	CreatedAt      time.Time          `bson:"created_at" json:"created_at"`
	LastVisitAt    time.Time          `bson:"last_visit_at" json:"last_visit_at"`
}
