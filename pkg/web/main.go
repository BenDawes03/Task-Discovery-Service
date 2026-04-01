package main

import (
	"fmt"
	"context"
	

	"tds/pkg/web/application"
)

func main() {
	app, err := application.New()
	if err != nil {
		fmt.Println("Failed to initialize application:", err)
		return
	}
	defer func() {
		_ = app.Close()
	}()

	if err := app.Start(context.TODO()); err != nil {
		fmt.Println("Failed to start app:", err)
	}
}

