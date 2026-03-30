package handler

import (
	"fmt"
	"net/http"
)

type Service struct{}

func (s *Service) Create(w http.ResponseWriter, r *http.Request) {
	fmt.Println("Create a service")
}

func (s *Service) List(w http.ResponseWriter, r *http.Request) {
	fmt.Println("List all services")
}
func (s *Service) GetById(w http.ResponseWriter, r *http.Request) {
	fmt.Println("Get an order by its id")
}

func (s *Service) UpdateById(w http.ResponseWriter, r *http.Request) {
	fmt.Println("Update a service")
}

func (s *Service) DeleteById(w http.ResponseWriter, r *http.Request) {
	fmt.Println("Delete a service")
}
