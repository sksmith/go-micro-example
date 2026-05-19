package testutil

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func Unmarshal(res *http.Response, v interface{}, t *testing.T) {
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	err = json.Unmarshal(body, v)
	if err != nil {
		t.Fatal(err)
	}
}

type RequestOptions struct {
	Token string
}

func Put(url string, request interface{}, t *testing.T, op ...RequestOptions) *http.Response {
	return SendRequest(http.MethodPut, url, request, t, op...)
}

func Post(url string, request interface{}, t *testing.T, op ...RequestOptions) *http.Response {
	return SendRequest(http.MethodPost, url, request, t, op...)
}

func SendRequest(method, url string, request interface{}, t *testing.T, op ...RequestOptions) *http.Response {
	json, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequest(method, url, bytes.NewBuffer(json))
	if err != nil {
		t.Fatal(err)
	}

	if len(op) > 0 && op[0].Token != "" {
		req.Header.Set("Authorization", "Bearer "+op[0].Token)
	}

	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	return res
}
