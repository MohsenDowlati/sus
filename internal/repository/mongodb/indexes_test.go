package mongodb

import (
	"reflect"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
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
