package transport

// CentralizedMessage represents a JSON message for centralized mode
type CentralizedMessage struct {
	Command string `json:"cmd"`           // "REGISTER" or "QUERY"
	Task    string `json:"task"`          // Task name
	Address string `json:"address,omitempty"` // Service address (for REGISTER)
}

// CentralizedResponse represents a JSON response from the server
type CentralizedResponse struct {
	Status  string   `json:"status"`           // "OK", "NOTFOUND", "ERR"
	Address string   `json:"address,omitempty"` // Service address (for QUERY success)
	Error   string   `json:"error,omitempty"`   // Error message (for ERR)
	Addresses []string `json:"addresses,omitempty"` // Multiple addresses (future extensibility)
}
