package main

import (
	"schedule_server/inits"
	"schedule_server/internal/app"
)

func main() {
	inits.Init()
	app.RunServer()
}
