package mongodb

import (
	"reflect"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

func TestLinkIndexModels_HasUniqueSlugIndex(t *testing.T) {
	wantKeys := bson.D{{Key: "code", Value: 1}}

	for _, model := range linkIndexModels() {
		keys, ok := model.Keys.(bson.D)
		if !ok || !reflect.DeepEqual(keys, wantKeys) {
			continue
		}
		if model.Options == nil || model.Options.Unique == nil || !*model.Options.Unique {
			t.Fatal("slug index on code exists but is not unique")
		}
		if model.Options.Name == nil || *model.Options.Name != "uniq_code" {
			t.Fatalf("slug index name = %v, want uniq_code", model.Options.Name)
		}
		return
	}

	t.Fatal("unique slug index on code field was not configured")
}

func TestClickEventIndexModels(t *testing.T) {
	models := clickEventIndexModels(30 * 24 * time.Hour)
	assertIndex(t, models, "code_timestamp", bson.D{{Key: "code", Value: 1}, {Key: "timestamp", Value: -1}}, false)
	assertIndex(t, models, "ttl_timestamp", bson.D{{Key: "timestamp", Value: 1}}, false)

	for _, model := range models {
		if model.Options != nil && model.Options.Name != nil && *model.Options.Name == "ttl_timestamp" {
			if model.Options.ExpireAfterSeconds == nil || *model.Options.ExpireAfterSeconds != 30*24*60*60 {
				t.Fatalf("TTL seconds = %v, want %d", model.Options.ExpireAfterSeconds, 30*24*60*60)
			}
			return
		}
	}
	t.Fatal("TTL index was not configured")
}

func TestClickEventIndexModelsOmitsTTLWhenRetentionDisabled(t *testing.T) {
	for _, model := range clickEventIndexModels(0) {
		if model.Options != nil && model.Options.Name != nil && *model.Options.Name == "ttl_timestamp" {
			t.Fatal("TTL index configured with disabled retention")
		}
	}
}

func TestLinkAnalyticsDailyIndexModels(t *testing.T) {
	assertIndex(
		t,
		linkAnalyticsDailyIndexModels(),
		"uniq_code_date",
		bson.D{{Key: "code", Value: 1}, {Key: "date", Value: 1}},
		true,
	)
}

func assertIndex(t *testing.T, models []mongo.IndexModel, name string, keys bson.D, unique bool) {
	t.Helper()
	for _, model := range models {
		modelKeys, ok := model.Keys.(bson.D)
		if !ok || !reflect.DeepEqual(modelKeys, keys) {
			continue
		}
		if model.Options == nil || model.Options.Name == nil || *model.Options.Name != name {
			t.Fatalf("index name = %v, want %s", model.Options.Name, name)
		}
		if unique && (model.Options.Unique == nil || !*model.Options.Unique) {
			t.Fatalf("index %s is not unique", name)
		}
		return
	}
	t.Fatalf("index %s with keys %v was not configured", name, keys)
}
