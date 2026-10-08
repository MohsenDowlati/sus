package analytics

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/MohsenDowlati/shorts/internal/repository/mongodb"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const dailyDateLayout = "2006-01-02"

type BatchProcessor interface {
	Process(context.Context, []redis.XMessage) error
}

type ClickEventDocument struct {
	ID        string    `bson:"_id"`
	Code      string    `bson:"code"`
	UserAgent string    `bson:"user_agent"`
	Referer   string    `bson:"referer"`
	IPHash    string    `bson:"ip_hash"`
	Timestamp time.Time `bson:"timestamp"`
}

type MongoBatchProcessor struct {
	client mongodb.Client
	events mongodb.Collection
	daily  mongodb.Collection
}

func NewMongoBatchProcessor(db mongodb.Database) *MongoBatchProcessor {
	return &MongoBatchProcessor{
		client: db.Client(),
		events: db.Collection(mongodb.ClickEventsCollection),
		daily:  db.Collection(mongodb.LinkAnalyticsDailyCollection),
	}
}

func (processor *MongoBatchProcessor) Process(ctx context.Context, messages []redis.XMessage) error {
	if len(messages) == 0 {
		return nil
	}

	events := make([]ClickEventDocument, 0, len(messages))
	for _, message := range messages {
		event, err := decodeClickEvent(message)
		if err != nil {
			return err
		}
		events = append(events, event)
	}

	session, err := processor.client.StartSession()
	if err != nil {
		return fmt.Errorf("start analytics transaction session: %w", err)
	}
	defer session.EndSession(ctx)

	_, err = session.WithTransaction(ctx, func(sessionCtx mongo.SessionContext) (interface{}, error) {
		newEvents, err := processor.persistRawEvents(sessionCtx, events)
		if err != nil {
			return nil, err
		}
		if err := processor.incrementDailySummaries(sessionCtx, newEvents); err != nil {
			return nil, err
		}
		return nil, nil
	})
	if err != nil {
		return fmt.Errorf("persist analytics batch transaction: %w", err)
	}
	return nil
}

func (processor *MongoBatchProcessor) persistRawEvents(ctx context.Context, events []ClickEventDocument) ([]ClickEventDocument, error) {
	models := make([]mongo.WriteModel, 0, len(events))
	for _, event := range events {
		models = append(models, mongo.NewUpdateOneModel().
			SetFilter(bson.M{"_id": event.ID}).
			SetUpdate(bson.M{"$setOnInsert": event}).
			SetUpsert(true))
	}

	result, err := processor.events.BulkWrite(ctx, models, options.BulkWrite().SetOrdered(false))
	if err != nil {
		return nil, fmt.Errorf("bulk persist click events: %w", err)
	}

	newEvents := make([]ClickEventDocument, 0, len(result.UpsertedIDs))
	for index := range result.UpsertedIDs {
		if index >= 0 && index < int64(len(events)) {
			newEvents = append(newEvents, events[index])
		}
	}
	return newEvents, nil
}

func (processor *MongoBatchProcessor) incrementDailySummaries(ctx context.Context, events []ClickEventDocument) error {
	increments := make(map[dailySummaryKey]bson.M)
	for _, event := range events {
		key := dailySummaryKey{
			Code: event.Code,
			Date: utcDate(event.Timestamp),
		}
		increment, exists := increments[key]
		if !exists {
			increment = bson.M{"total_clicks": int64(0)}
			increments[key] = increment
		}
		referrerField := "referrers." + normalizeReferrer(event.Referer)
		browserField := "browsers." + normalizeBrowser(event.UserAgent)
		increment["total_clicks"] = increment["total_clicks"].(int64) + 1
		increment[referrerField] = incrementValue(increment, referrerField)
		increment[browserField] = incrementValue(increment, browserField)
	}

	models := make([]mongo.WriteModel, 0, len(increments))
	for key, increment := range increments {
		models = append(models, mongo.NewUpdateOneModel().
			SetFilter(bson.M{"code": key.Code, "date": key.Date}).
			SetUpdate(bson.M{
				"$setOnInsert": bson.M{"code": key.Code, "date": key.Date},
				"$inc":         increment,
			}).
			SetUpsert(true))
	}
	if len(models) == 0 {
		return nil
	}
	if _, err := processor.daily.BulkWrite(ctx, models, options.BulkWrite().SetOrdered(false)); err != nil {
		return fmt.Errorf("increment daily analytics: %w", err)
	}
	return nil
}

func utcDate(timestamp time.Time) string {
	return timestamp.UTC().Format(dailyDateLayout)
}

type dailySummaryKey struct {
	Code string
	Date string
}

func incrementValue(increment bson.M, field string) int64 {
	value, _ := increment[field].(int64)
	return value + 1
}

func decodeClickEvent(message redis.XMessage) (ClickEventDocument, error) {
	code := strings.TrimSpace(valueString(message.Values["code"]))
	if code == "" {
		return ClickEventDocument{}, fmt.Errorf("decode click event %s: code is required", message.ID)
	}
	timestampValue := valueString(message.Values["ts"])
	timestamp, err := time.Parse(time.RFC3339Nano, timestampValue)
	if err != nil {
		return ClickEventDocument{}, fmt.Errorf("decode click event %s timestamp: %w", message.ID, err)
	}
	return ClickEventDocument{
		ID:        message.ID,
		Code:      code,
		UserAgent: valueString(message.Values["ua"]),
		Referer:   normalizeRawReferer(valueString(message.Values["ref"])),
		IPHash:    valueString(message.Values["ip"]),
		Timestamp: timestamp.UTC(),
	}, nil
}

func normalizeRawReferer(referer string) string {
	referer = strings.TrimSpace(referer)
	if referer == "" {
		return "direct"
	}
	return referer
}

func normalizeReferrer(referer string) string {
	if strings.TrimSpace(referer) == "" || strings.EqualFold(strings.TrimSpace(referer), "direct") {
		return "direct"
	}

	parsed, err := url.Parse(strings.TrimSpace(referer))
	if err != nil || parsed.Hostname() == "" {
		parsed, err = url.Parse("//" + strings.TrimSpace(referer))
	}
	if err != nil {
		return "other"
	}
	host := strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
	switch {
	case host == "t.co", host == "x.com", host == "twitter.com", strings.HasSuffix(host, ".twitter.com"):
		return "twitter"
	case host == "lnkd.in", host == "linkedin.com", strings.HasSuffix(host, ".linkedin.com"):
		return "linkedin"
	case host == "fb.me", host == "facebook.com", strings.HasSuffix(host, ".facebook.com"):
		return "facebook"
	case host == "instagram.com", strings.HasSuffix(host, ".instagram.com"):
		return "instagram"
	case host == "reddit.com", strings.HasSuffix(host, ".reddit.com"):
		return "reddit"
	case host == "youtu.be", host == "youtube.com", strings.HasSuffix(host, ".youtube.com"):
		return "youtube"
	case host == "google.com", strings.HasPrefix(host, "google."), strings.Contains(host, ".google."):
		return "google"
	case host == "bing.com", strings.HasSuffix(host, ".bing.com"):
		return "bing"
	case host == "yahoo.com", strings.HasSuffix(host, ".yahoo.com"):
		return "yahoo"
	default:
		return "other"
	}
}

func normalizeBrowser(userAgent string) string {
	userAgent = strings.ToLower(userAgent)
	switch {
	case strings.Contains(userAgent, "bot"), strings.Contains(userAgent, "crawler"), strings.Contains(userAgent, "spider"):
		return "bot"
	case strings.Contains(userAgent, "edg/"):
		return "edge"
	case strings.Contains(userAgent, "opr/"), strings.Contains(userAgent, "opera"):
		return "opera"
	case strings.Contains(userAgent, "firefox/"), strings.Contains(userAgent, "fxios/"):
		return "firefox"
	case strings.Contains(userAgent, "chrome/"), strings.Contains(userAgent, "crios/"):
		return "chrome"
	case strings.Contains(userAgent, "safari/"):
		return "safari"
	case strings.Contains(userAgent, "msie"), strings.Contains(userAgent, "trident/"):
		return "internet_explorer"
	default:
		return "other"
	}
}

func valueString(value interface{}) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []byte:
		return string(typed)
	case nil:
		return ""
	default:
		return fmt.Sprint(typed)
	}
}
