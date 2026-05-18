package app

import (
	"net/http"

	"github.com/sksmith/go-micro-example/config"
	"github.com/sksmith/go-micro-example/internal/platform/httpx"
)

type EnvResponse struct {
	config.Config
} // @name EnvResponse

func NewEnvResponse(c config.Config) *EnvResponse {
	resp := &EnvResponse{Config: c}
	return resp
}

func (er *EnvResponse) Render(_ http.ResponseWriter, _ *http.Request) error {
	httpx.Scrub(er)
	return nil
}
