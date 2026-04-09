package main

import (
	"fmt"
	"context"
	"os"
	"os/signal"

	"tds/pkg/web/application"
)

func main() {
	app, err := application.New()
	
	if err != nil {
		fmt.Println("Failed to initialize application:", err)
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	defer func() { _ = app.Close() }()

	if err := app.Start(ctx); err != nil {
		fmt.Println("Failed to start app:", err)
	}
}

