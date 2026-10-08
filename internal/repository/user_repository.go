package repository

import (
	"context"
	"errors"
	"github.com/MohsenDowlati/shorts/internal/repository/mongodb"

	"github.com/MohsenDowlati/shorts/internal/domain"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// UserRepository persists users in MongoDB via the config.Collection abstraction.
type UserRepository struct {
	coll mongodb.Collection
}

func NewUserRepository(db mongodb.Database) *UserRepository {
	return &UserRepository{coll: db.Collection(mongodb.UsersCollection)}
}

// Create inserts a new user. It maps a duplicate-key violation on the unique
// username index to domain.ErrUsernameTaken.
func (r *UserRepository) Create(ctx context.Context, user *domain.User) error {
	if _, err := r.coll.InsertOne(ctx, user); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return domain.ErrUsernameTaken
		}
		return err
	}
	return nil
}

// GetByUsername looks up a user by username, returning domain.ErrUserNotFound
// when no document matches.
func (r *UserRepository) GetByUsername(ctx context.Context, username string) (*domain.User, error) {
	var user domain.User
	err := r.coll.FindOne(ctx, bson.M{"username": username}).Decode(&user)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, domain.ErrUserNotFound
		}
		return nil, err
	}
	return &user, nil
}
