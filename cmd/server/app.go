package server

import (
	"github.com/MohsenDowlati/shorts/internal/config"
	"github.com/MohsenDowlati/shorts/internal/repository/mongodb"
)

type Application struct {
	Env   *config.Env
	Mongo mongodb.Client
}

func AppWithEnv(env *config.Env) Application {
	app := &Application{Env: env}
	app.Mongo = config.NewMongoDatabase(app.Env)
	return *app
}

func (app *Application) CloseDBConnection() {
	config.CloseMongoDBConnection(app.Mongo)
}
