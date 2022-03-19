package testutil

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"net"
	"net/http"
	"testing"

	"github.com/gobwas/ws/wsutil"
)

func Unmarshal(res *http.Response, v interface{}, t *testing.T) {
	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	err = json.Unmarshal(body, v)
	if err != nil {
		t.Fatal(err)
	}
}

type RequestOptions struct {
	Username string
	Password string
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

	if len(op) > 0 {
		req.SetBasicAuth(op[0].Username, op[0].Password)
	}

	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	return res
}

func ReadWs(conn net.Conn, v interface{}, t *testing.T) {
	msg, _, err := wsutil.ReadServerData(conn)
	if err != nil {
		t.Fatal(err)
	}

	err = json.Unmarshal(msg, v)
	if err != nil {
		t.Fatal(err)
	}
}
