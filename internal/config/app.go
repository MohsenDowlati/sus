package config

type Application struct {
	Env   *Env
	Mongo Client
}

func App() Application {
	return AppWithEnv(NewEnv())
}

func AppWithEnv(env *Env) Application {
	app := &Application{Env: env}
	app.Mongo = NewMongoDatabase(app.Env)
	return *app
}

func (app *Application) CloseDBConnection() {
	CloseMongoDBConnection(app.Mongo)
}
