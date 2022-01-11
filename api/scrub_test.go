package api_test

import (
	"testing"

	"github.com/sksmith/go-micro-example/api"
)

type TestObj struct {
	PlainText  string
	SensText   string `sensitive:"true"`
	PlainInt   int
	SensInt    int `sensitive:"true"`
	PlainFloat float32
	SensFloat  float32 `sensitive:"true"`
	SensBool   bool    `sensitive:"true"`
}

func TestScrub(t *testing.T) {
	tests := []struct {
		input TestObj
		want  TestObj
	}{
		{
			input: TestObj{PlainText: "plaintext", SensText: "abc", PlainInt: 123, SensInt: 123, PlainFloat: 1.23, SensFloat: 1.23, SensBool: true},
			want:  TestObj{PlainText: "plaintext", SensText: "******", PlainInt: 123, SensInt: 0, PlainFloat: 1.23, SensFloat: 0.00, SensBool: false},
		},
	}

	for _, test := range tests {
		api.Scrub(&test.input)
		expect(test.input, test.want, t)
	}
}

func expect(got, want interface{}, t *testing.T) {
	if got != want {
		t.Errorf("\n got=[%+v]\nwant=[%+v]", got, want)
	}
}
