package repository

import (
	"context"
	"fmt"

	"github.com/MohsenDowlati/shorts/internal/domain"
	"github.com/MohsenDowlati/shorts/internal/repository/mongodb"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type analyticsCollection interface {
	Aggregate(context.Context, interface{}) (mongodb.Cursor, error)
}

type LinkAnalyticsRepository struct {
	collection analyticsCollection
}

func NewLinkAnalyticsRepository(db mongodb.Database) *LinkAnalyticsRepository {
	return &LinkAnalyticsRepository{collection: db.Collection(mongodb.LinkAnalyticsDailyCollection)}
}

func (repository *LinkAnalyticsRepository) Get(ctx context.Context, code, from, to string) (*domain.LinkAnalytics, error) {
	cursor, err := repository.collection.Aggregate(ctx, linkAnalyticsPipeline(code, from, to))
	if err != nil {
		return nil, fmt.Errorf("aggregate link analytics: %w", err)
	}
	defer func() { _ = cursor.Close(context.Background()) }()

	var results []struct {
		Daily []domain.DailyClickTotal `bson:"daily"`
		Total []struct {
			TotalClicks int64 `bson:"total_clicks"`
		} `bson:"total"`
		Referrers []struct {
			Name  string `bson:"name"`
			Count int64  `bson:"count"`
		} `bson:"referrers"`
		Browsers []struct {
			Name  string `bson:"name"`
			Count int64  `bson:"count"`
		} `bson:"browsers"`
	}
	if err := cursor.All(ctx, &results); err != nil {
		return nil, fmt.Errorf("decode link analytics: %w", err)
	}

	analytics := &domain.LinkAnalytics{
		Code:      code,
		From:      from,
		To:        to,
		Daily:     []domain.DailyClickTotal{},
		Referrers: map[string]int64{},
		Browsers:  map[string]int64{},
	}
	if len(results) == 0 {
		return analytics, nil
	}
	analytics.Daily = results[0].Daily
	if analytics.Daily == nil {
		analytics.Daily = []domain.DailyClickTotal{}
	}
	if len(results[0].Total) > 0 {
		analytics.TotalClicks = results[0].Total[0].TotalClicks
	}
	for _, statistic := range results[0].Referrers {
		analytics.Referrers[statistic.Name] = statistic.Count
	}
	for _, statistic := range results[0].Browsers {
		analytics.Browsers[statistic.Name] = statistic.Count
	}
	return analytics, nil
}

func linkAnalyticsPipeline(code, from, to string) mongo.Pipeline {
	return mongo.Pipeline{
		{{Key: "$match", Value: bson.M{
			"code": code,
			"date": bson.M{"$gte": from, "$lte": to},
		}}},
		{{Key: "$facet", Value: bson.M{
			"daily": mongo.Pipeline{
				{{Key: "$sort", Value: bson.D{{Key: "date", Value: 1}}}},
				{{Key: "$project", Value: bson.M{"_id": 0, "date": 1, "total_clicks": 1}}},
			},
			"total": mongo.Pipeline{
				{{Key: "$group", Value: bson.M{"_id": nil, "total_clicks": bson.M{"$sum": "$total_clicks"}}}},
				{{Key: "$project", Value: bson.M{"_id": 0, "total_clicks": 1}}},
			},
			"referrers": statisticPipeline("referrers"),
			"browsers":  statisticPipeline("browsers"),
		}}},
	}
}

func statisticPipeline(field string) mongo.Pipeline {
	return mongo.Pipeline{
		{{Key: "$project", Value: bson.M{
			"entries": bson.M{"$objectToArray": bson.M{"$ifNull": bson.A{"$" + field, bson.M{}}}},
		}}},
		{{Key: "$unwind", Value: "$entries"}},
		{{Key: "$group", Value: bson.M{"_id": "$entries.k", "count": bson.M{"$sum": "$entries.v"}}}},
		{{Key: "$project", Value: bson.M{"_id": 0, "name": "$_id", "count": 1}}},
		{{Key: "$sort", Value: bson.D{{Key: "count", Value: -1}, {Key: "name", Value: 1}}}},
	}
}
