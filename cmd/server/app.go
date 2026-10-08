package server

import (
	"context"
	"errors"
	"fmt"

	"github.com/MohsenDowlati/shorts/internal/config"
	"github.com/MohsenDowlati/shorts/internal/repository/mongodb"
	"github.com/redis/go-redis/v9"
)

type Application struct {
	Env   *config.Env
	Mongo mongodb.Client
	Redis *redis.Client
}

func AppWithEnv(ctx context.Context, env *config.Env) (*Application, error) {
	app := &Application{Env: env}
	app.Mongo = config.NewMongoDatabase(app.Env)

	redisClient, err := config.NewRedisClient(ctx, app.Env)
	if err != nil {
		closeErr := config.CloseMongoDBConnection(ctx, app.Mongo)
		return nil, errors.Join(err, closeErr)
	}
	app.Redis = redisClient
	return app, nil
}

func (app *Application) Close(ctx context.Context) error {
	if app == nil {
		return nil
	}
	return errors.Join(
		wrapCloseError("Redis", config.CloseRedisClient(app.Redis)),
		wrapCloseError("MongoDB", config.CloseMongoDBConnection(ctx, app.Mongo)),
	)
}

func wrapCloseError(component string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("close %s connection: %w", component, err)
}
