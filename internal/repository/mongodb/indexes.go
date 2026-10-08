package mongodb

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Collection names, kept in one place so repositories and index setup agree.
const (
	LinksCollection              = "links"
	UsersCollection              = "users"
	ClickEventsCollection        = "click_events"
	LinkAnalyticsDailyCollection = "link_analytics_daily"
)

// EnsureIndexes creates every index the application relies on. It is safe to
// call on each startup: CreateMany is idempotent as long as an index's
// keys/options match what already exists, so re-running is a no-op.
func EnsureIndexes(ctx context.Context, db Database) error {
	return EnsureIndexesWithClickRetention(ctx, db, 0)
}

func EnsureIndexesWithClickRetention(ctx context.Context, db Database, rawRetention time.Duration) error {
	if err := ensureLinkIndexes(ctx, db.Collection(LinksCollection)); err != nil {
		return err
	}
	if err := ensureUserIndexes(ctx, db.Collection(UsersCollection)); err != nil {
		return err
	}
	if err := ensureClickEventIndexes(ctx, db.Collection(ClickEventsCollection), rawRetention); err != nil {
		return err
	}
	if err := ensureLinkAnalyticsDailyIndexes(ctx, db.Collection(LinkAnalyticsDailyCollection)); err != nil {
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

func ensureClickEventIndexes(ctx context.Context, events Collection, rawRetention time.Duration) error {
	ttlReady, err := reconcileClickEventIndexes(ctx, events, rawRetention)
	if err != nil {
		return err
	}
	models := clickEventIndexModels(rawRetention)
	if ttlReady {
		models = models[:1]
	}
	if _, err := events.Indexes().CreateMany(ctx, models); err != nil {
		return fmt.Errorf("create %s indexes: %w", ClickEventsCollection, err)
	}
	return nil
}

func reconcileClickEventIndexes(ctx context.Context, events Collection, rawRetention time.Duration) (bool, error) {
	indexes := events.Indexes()
	cursor, err := indexes.List(ctx)
	if err != nil {
		var commandErr mongo.CommandError
		if errors.As(err, &commandErr) && commandErr.Code == 26 {
			return false, nil
		}
		return false, fmt.Errorf("list %s indexes: %w", ClickEventsCollection, err)
	}
	defer func() { _ = cursor.Close(context.Background()) }()

	var specifications []struct {
		Name               string `bson:"name"`
		ExpireAfterSeconds *int64 `bson:"expireAfterSeconds,omitempty"`
	}
	if err := cursor.All(ctx, &specifications); err != nil {
		return false, fmt.Errorf("decode %s indexes: %w", ClickEventsCollection, err)
	}

	desiredTTL := int64(rawRetention / time.Second)
	ttlReady := false
	for _, specification := range specifications {
		switch specification.Name {
		case "uniq_stream_id":
			if _, err := indexes.DropOne(ctx, specification.Name); err != nil {
				return false, fmt.Errorf("drop legacy %s index: %w", ClickEventsCollection, err)
			}
		case "ttl_timestamp":
			if rawRetention > 0 && specification.ExpireAfterSeconds != nil && *specification.ExpireAfterSeconds == desiredTTL {
				ttlReady = true
				continue
			}
			if _, err := indexes.DropOne(ctx, specification.Name); err != nil {
				return false, fmt.Errorf("replace %s TTL index: %w", ClickEventsCollection, err)
			}
		}
	}
	return ttlReady, nil
}

func clickEventIndexModels(rawRetention time.Duration) []mongo.IndexModel {
	models := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "code", Value: 1}, {Key: "timestamp", Value: -1}},
			Options: options.Index().SetName("code_timestamp"),
		},
	}
	if rawRetention > 0 {
		models = append(models, mongo.IndexModel{
			Keys: bson.D{{Key: "timestamp", Value: 1}},
			Options: options.Index().
				SetName("ttl_timestamp").
				SetExpireAfterSeconds(int32(rawRetention / time.Second)),
		})
	}
	return models
}

func ensureLinkAnalyticsDailyIndexes(ctx context.Context, daily Collection) error {
	if _, err := daily.Indexes().CreateMany(ctx, linkAnalyticsDailyIndexModels()); err != nil {
		return fmt.Errorf("create %s indexes: %w", LinkAnalyticsDailyCollection, err)
	}
	return nil
}

func linkAnalyticsDailyIndexModels() []mongo.IndexModel {
	return []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "code", Value: 1}, {Key: "date", Value: 1}},
			Options: options.Index().SetName("uniq_code_date").SetUnique(true),
		},
	}
}
