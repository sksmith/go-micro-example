package api

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
		if f.Kind() == reflect.Struct {
			Scrub(v.Field(i).Addr().Interface())
		}
		if sf.Tag.Get("sensitive") != "" {
			switch f.Kind() {
			case reflect.String:
				f.SetString("******")
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
				reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
				f.SetInt(0)
			case reflect.Float32, reflect.Float64:
				f.SetFloat(0.00)
			default:
				log.Warn().
					Str("fieldName", sf.Name).
					Str("type", f.Kind().String()).
					Msg("field marked sensitive but was an unrecognized type")
			}
		}
	}
}
