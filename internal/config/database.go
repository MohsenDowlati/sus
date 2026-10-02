package config

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"
)

func NewMongoDatabase(env *Env) Client {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mongodbURI := buildMongoURI(env)
	if mongodbURI == "" {
		log.Fatal("MongoDB configuration missing: provide either MONGODB_URI or DB_HOST/DB_PORT")
	}

	client, err := NewClient(mongodbURI)
	if err != nil {
		log.Fatal(err)
	}

	err = client.Connect(ctx)
	if err != nil {
		log.Fatal(err)
	}

	err = client.Ping(ctx)
	if err != nil {
		log.Fatal(err)
	}

	if err = EnsureIndexes(ctx, client.Database(env.DBName)); err != nil {
		log.Fatalf("ensure indexes: %v", err)
	}

	return client
}

func CloseMongoDBConnection(client Client) {
	if client == nil {
		return
	}

	err := client.Disconnect(context.TODO())
	if err != nil {
		log.Fatal(err)
	}

	log.Println("Connection to MongoDB closed.")
}

func buildMongoURI(env *Env) string {
	if strings.TrimSpace(env.MongoURI) != "" {
		return strings.TrimSpace(env.MongoURI)
	}

	if strings.TrimSpace(env.DBHost) == "" {
		return ""
	}

	hostPort := strings.TrimSpace(env.DBHost)
	if strings.TrimSpace(env.DBPort) != "" {
		hostPort = fmt.Sprintf("%s:%s", hostPort, strings.TrimSpace(env.DBPort))
	}

	dbPath := ""
	if strings.TrimSpace(env.DBName) != "" {
		dbPath = "/" + strings.TrimSpace(env.DBName)
	}

	if strings.TrimSpace(env.DBUser) != "" && strings.TrimSpace(env.DBPass) != "" {
		authSource := strings.TrimSpace(env.DBAuthSource)
		user := url.QueryEscape(strings.TrimSpace(env.DBUser))
		pass := url.QueryEscape(strings.TrimSpace(env.DBPass))
		query := ""
		if authSource != "" {
			query = fmt.Sprintf("?authSource=%s", url.QueryEscape(authSource))
		}
		return fmt.Sprintf("mongodb://%s:%s@%s%s%s", user, pass, hostPort, dbPath, query)
	}

	return fmt.Sprintf("mongodb://%s%s", hostPort, dbPath)
}
