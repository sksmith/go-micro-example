package httpx

import (
	"reflect"

	"github.com/rs/zerolog/log"
)

func Scrub(o interface{}) {
	v := reflect.ValueOf(o).Elem()
	t := reflect.TypeOf(o).Elem()
	if v.Kind() != reflect.Struct {
		return
	}

	for i := 0; i < t.NumField(); i++ {
		f := v.Field(i)
		sf := t.Field(i)
		tagged := sf.Tag.Get("sensitive") != ""

		if tagged {
			redact(f, sf.Name)
			continue
		}
		if f.Kind() == reflect.Struct {
			Scrub(v.Field(i).Addr().Interface())
		}
	}
}

// redact zeroes a leaf field, or — for the wrapped *Config types in this
// project — zeroes the inner Value field of a tagged struct.
func redact(f reflect.Value, name string) {
	switch f.Kind() {
	case reflect.String:
		f.SetString("******")
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		f.SetInt(0)
	case reflect.Float32, reflect.Float64:
		f.SetFloat(0.00)
	case reflect.Bool:
		f.SetBool(false)
	case reflect.Struct:
		inner := f.FieldByName("Value")
		if inner.IsValid() && inner.CanSet() {
			redact(inner, name+".Value")
			return
		}
		log.Warn().
			Str("fieldName", name).
			Str("type", f.Kind().String()).
			Msg("field marked sensitive but is a struct without a Value field")
	default:
		log.Warn().
			Str("fieldName", name).
			Str("type", f.Kind().String()).
			Msg("field marked sensitive but was an unrecognized type")
	}
}
