package mongodb

import (
	"context"
	"testing"

	"github.com/MohsenDowlati/shorts/internal/metrics"
	dto "github.com/prometheus/client_model/go"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

func databaseMetric(operation, collection string) float64 {
	m := &dto.Metric{}
	_ = metrics.DBOperations.WithLabelValues(operation, collection).Write(m)
	return m.GetCounter().GetValue()
}

func TestDatabaseMetricsCountFailedAttemptsAndBulkKinds(t *testing.T) {
	client, err := mongo.NewClient()
	if err != nil {
		t.Fatal(err)
	}
	// A disconnected client makes calls fail without requiring a live database.
	collection := &mongoCollection{coll: client.Database("shorts").Collection("links")}
	ctx := context.Background()
	inserts := databaseMetric("insert", "links")
	finds := databaseMetric("find", "links")
	updates := databaseMetric("update", "links")
	_, _ = collection.InsertOne(ctx, bson.M{"code": "abc"})
	_ = collection.FindOne(ctx, bson.M{"code": "abc"}).Decode(&bson.M{})
	_, _ = collection.UpdateOne(ctx, bson.M{}, bson.M{"$set": bson.M{"is_disabled": true}})
	if databaseMetric("insert", "links") != inserts+1 || databaseMetric("find", "links") != finds+1 || databaseMetric("update", "links") != updates+1 {
		t.Fatal("failed MongoDB calls must be counted")
	}
	events := &mongoCollection{coll: client.Database("shorts").Collection("click_events")}
	eventInserts := databaseMetric("insert", "click_events")
	eventUpdates := databaseMetric("update", "click_events")
	_, _ = events.BulkWrite(ctx, []mongo.WriteModel{mongo.NewInsertOneModel().SetDocument(bson.M{}), mongo.NewUpdateOneModel(), mongo.NewUpdateOneModel()})
	if databaseMetric("insert", "click_events") != eventInserts+1 || databaseMetric("update", "click_events") != eventUpdates+1 {
		t.Fatal("bulk must count each operation kind once")
	}
}
