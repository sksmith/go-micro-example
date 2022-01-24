package api

import (
	"net/http"

	"github.com/sksmith/go-micro-example/config"
)

type EnvResponse struct {
	config.Config
}

func NewEnvResponse(c config.Config) *EnvResponse {
	resp := &EnvResponse{Config: c}
	return resp
}

func (er *EnvResponse) Render(_ http.ResponseWriter, _ *http.Request) error {
	Scrub(er)
	return nil
}
