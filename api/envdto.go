package api

// TODO
// import (
// 	"net/http"

// 	"github.com/sksmith/note-server/config"
// )

// type EnvResponse struct {
// 	config.Config

// 	// we don't want to display sensitive configuration data
// 	// outside of the service
// 	ProtectedQUser  string `json:"qUser"`
// 	ProtectedQPass  string `json:"qPass"`
// 	ProtectedDbUser string `json:"dbUser"`
// 	ProtectedDbPass string `json:"dbPass"`
// }

// func NewEnvResponse(c config.Config) *EnvResponse {
// 	resp := &EnvResponse{Config: c}
// 	return resp
// }

// func (er *EnvResponse) Render(_ http.ResponseWriter, _ *http.Request) error {
// 	er.Config.QUser = "******"
// 	er.Config.QPass = "******"
// 	er.Config.DbUser = "******"
// 	er.Config.DbPass = "******"
// 	return nil
// }
