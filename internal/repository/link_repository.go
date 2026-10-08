package repository

import (
	"context"
	"errors"

	"github.com/MohsenDowlati/shorts/internal/domain"
	"github.com/MohsenDowlati/shorts/internal/repository/mongodb"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type LinkRepository struct {
	collection mongodb.Collection
}

func NewLinkRepository(db mongodb.Database) *LinkRepository {
	return &LinkRepository{collection: db.Collection(mongodb.LinksCollection)}
}

func (lr *LinkRepository) Create(ctx context.Context, link *domain.Link) error {
	if _, err := lr.collection.InsertOne(ctx, link); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return domain.ErrSlugReserved
		}
		return err
	}
	return nil
}

func (lr *LinkRepository) GetByCode(ctx context.Context, code string) (*domain.Link, error) {
	var link domain.Link
	if err := lr.collection.FindOne(ctx, bson.M{"code": code}).Decode(&link); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, domain.ErrLinkNotFound
		}
		return nil, err
	}
	return &link, nil
}
