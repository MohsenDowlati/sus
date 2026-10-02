package domain

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type LinkType string

const (
	LinkTypeAuto   LinkType = "auto"
	LinkTypeCustom LinkType = "custom"
)

type Link struct {
	ID          primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Code        string             `bson:"code" json:"code"`
	OriginalURL string             `bson:"original_url" json:"original_url"`
	Type        LinkType           `bson:"type" json:"type"`
	OwnerID     *string            `bson:"owner_id,omitempty" json:"owner_id,omitempty"`
	CreatedAt   time.Time          `bson:"created_at" json:"created_at"`
	ExpiresAt   *time.Time         `bson:"expires_at,omitempty" json:"expires_at,omitempty"`
	IsDisabled  bool               `bson:"is_disabled" json:"is_disabled"`
}

type ClickEvent struct {
	ID        primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Code      string             `bson:"code" json:"code"`
	Timestamp time.Time          `bson:"timestamp" json:"timestamp"`
	IPHash    string             `bson:"ip_hash" json:"-"`
	Country   string             `bson:"country" json:"country"`
	Referrer  string             `bson:"referrer" json:"referrer"`
	Browser   string             `bson:"browser" json:"browser"`
	OS        string             `bson:"os" json:"os"`
}
