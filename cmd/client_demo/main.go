package main

import (
    "fmt"
    "time"

    "tds/pkg/client"
)

func main() {
    server := "127.0.0.1:5000"
    task := "demo"
    addr := "127.0.0.1:12345"

    fmt.Println("Registering", task, "->", addr)
    if err := client.Register(server, task, addr); err != nil {
        fmt.Println("Register error:", err)
        return
    }

    // brief pause to allow server processing
    time.Sleep(200 * time.Millisecond)

    resp, err := client.Query(server, task)
    if err != nil {
        fmt.Println("Query error:", err)
        return
    }
    fmt.Println("Query response:", resp)
}
