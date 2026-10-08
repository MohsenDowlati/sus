package mongodb

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Collection names, kept in one place so repositories and index setup agree.
const (
	LinksCollection = "links"
	UsersCollection = "users"
)

// EnsureIndexes creates every index the application relies on. It is safe to
// call on each startup: CreateMany is idempotent as long as an index's
// keys/options match what already exists, so re-running is a no-op.
func EnsureIndexes(ctx context.Context, db Database) error {
	if err := ensureLinkIndexes(ctx, db.Collection(LinksCollection)); err != nil {
		return err
	}
	if err := ensureUserIndexes(ctx, db.Collection(UsersCollection)); err != nil {
		return err
	}
	return nil
}

// ensureLinkIndexes creates, on the links collection:
//   - uniq_code       {code: 1} unique — slugs/custom codes are globally unique.
//   - owner_created   {owner_id: 1, created_at: -1} — per-user dashboard listings.
//   - ttl_expires_at  {expires_at: 1} expireAfterSeconds: 0 — native TTL; a link
//     is deleted once its expires_at passes. Links without expires_at never expire.
func ensureLinkIndexes(ctx context.Context, links Collection) error {
	if _, err := links.Indexes().CreateMany(ctx, linkIndexModels()); err != nil {
		return fmt.Errorf("create %s indexes: %w", LinksCollection, err)
	}
	return nil
}

func linkIndexModels() []mongo.IndexModel {
	return []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "code", Value: 1}},
			Options: options.Index().SetName("uniq_code").SetUnique(true),
		},
		{
			Keys:    bson.D{{Key: "owner_id", Value: 1}, {Key: "created_at", Value: -1}},
			Options: options.Index().SetName("owner_created"),
		},
		{
			Keys:    bson.D{{Key: "expires_at", Value: 1}},
			Options: options.Index().SetName("ttl_expires_at").SetExpireAfterSeconds(0),
		},
	}
}

// ensureUserIndexes creates, on the users collection:
//   - uniq_username   {username: 1} unique — guards against duplicate accounts
//     and backs the duplicate-key check in the signup path.
func ensureUserIndexes(ctx context.Context, users Collection) error {
	models := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "username", Value: 1}},
			Options: options.Index().SetName("uniq_username").SetUnique(true),
		},
	}
	if _, err := users.Indexes().CreateMany(ctx, models); err != nil {
		return fmt.Errorf("create %s indexes: %w", UsersCollection, err)
	}
	return nil
}
