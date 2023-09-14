package main

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/mitchellh/mapstructure"

	"github.com/lxc/incus/internal/i18n"
)

// stringToTimeHookFunc is a custom decoding hook that converts string values to time.Time using the given layout.
func stringToTimeHookFunc(layout string) mapstructure.DecodeHookFuncType {
	return func(from reflect.Type, to reflect.Type, data interface{}) (interface{}, error) {
		if from.Kind() == reflect.String && to == reflect.TypeOf(time.Time{}) {
			strValue := data.(string)
			t, err := time.Parse(layout, strValue)
			if err != nil {
				return nil, err
			}

			return t, nil
		}

		return data, nil
	}
}

// stringToBoolHookFunc is a custom decoding hook that converts string values to bool.
func stringToBoolHookFunc() mapstructure.DecodeHookFunc {
	return func(f reflect.Kind, t reflect.Kind, data interface{}) (interface{}, error) {
		if f != reflect.String || t != reflect.Bool {
			return data, nil
		}

		str := data.(string)
		str = strings.ToLower(str)
		switch str {
		case "1", "t", "true":
			return true, nil
		case "0", "f", "false":
			return false, nil
		default:
			return false, fmt.Errorf("Invalid boolean value: %s", str)
		}
	}
}

// stringToIntHookFunc is a custom decoding hook that converts string values to int.
func stringToIntHookFunc() mapstructure.DecodeHookFunc {
	return func(f reflect.Kind, t reflect.Kind, data interface{}) (interface{}, error) {
		if f != reflect.String || (t != reflect.Int && t != reflect.Int8 && t != reflect.Int16 && t != reflect.Int32 && t != reflect.Int64) {
			return data, nil
		}

		str := data.(string)
		value, err := strconv.Atoi(str)
		if err != nil {
			return data, err
		}

		return value, nil
	}
}

// stringToFloatHookFunc is a custom decoding hook that converts string values to float.
func stringToFloatHookFunc() mapstructure.DecodeHookFunc {
	return func(f reflect.Kind, t reflect.Kind, data interface{}) (interface{}, error) {
		if f != reflect.String || (t != reflect.Float32 && t != reflect.Float64) {
			return data, nil
		}

		str := data.(string)
		value, err := strconv.ParseFloat(str, 64)
		if err != nil {
			return data, err
		}

		return value, nil
	}
}

// getFieldByJsonTag gets the value of a struct field by its JSON tag.
func getFieldByJsonTag(obj any, tag string) (any, error) {
	var res any
	ok := false
	v := reflect.ValueOf(obj).Elem()
	for i := 0; i < v.NumField(); i++ {
		jsonTag := v.Type().Field(i).Tag.Get("json")

		// Ignore any options that might be specified after a comma in the tag.
		commaIdx := strings.Index(jsonTag, ",")
		if commaIdx > 0 {
			jsonTag = jsonTag[:commaIdx]
		}

		if strings.EqualFold(jsonTag, tag) {
			res = v.Field(i).Interface()
			ok = true
		}
	}

	if !ok {
		return nil, fmt.Errorf("The property with tag %q does not exist", tag)
	}

	return res, nil
}

// setFieldByJsonTag sets the value of a struct field by its JSON tag.
func setFieldByJsonTag(obj any, tag string, value any) {
	v := reflect.ValueOf(obj).Elem()
	var fieldName string

	for i := 0; i < v.NumField(); i++ {
		jsonTag := v.Type().Field(i).Tag.Get("json")
		commaIdx := strings.Index(jsonTag, ",")
		if commaIdx > 0 {
			jsonTag = jsonTag[:commaIdx]
		}

		if strings.EqualFold(jsonTag, tag) {
			fieldName = v.Type().Field(i).Name
		}
	}

	if fieldName != "" {
		if v.FieldByName(fieldName).CanSet() {
			v.FieldByName(fieldName).Set(reflect.ValueOf(value))
		}
	}
}

// unsetFieldByJsonTag unsets (give a default value) the value of a struct field by its JSON tag.
func unsetFieldByJsonTag(obj any, tag string) error {
	v, err := getFieldByJsonTag(obj, tag)
	if err != nil {
		return err
	}

	switch v.(type) {
	case string:
		setFieldByJsonTag(obj, tag, "")
	case int:
		setFieldByJsonTag(obj, tag, 0)
	case bool:
		setFieldByJsonTag(obj, tag, false)
	case float32, float64:
		setFieldByJsonTag(obj, tag, 0.0)
	case time.Time:
		setFieldByJsonTag(obj, tag, time.Time{})
	case *time.Time:
		setFieldByJsonTag(obj, tag, &time.Time{})
	}

	return nil
}

// unpackKVToWritable unpacks a map[string]string into a writable API struct.
func unpackKVToWritable(writable any, keys map[string]string) error {
	data := make(map[string]any)
	for k, v := range keys {
		data[k] = v
	}

	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		TagName: "json",
		Result:  writable,
		DecodeHook: mapstructure.ComposeDecodeHookFunc(
			stringToBoolHookFunc(),
			stringToIntHookFunc(),
			stringToFloatHookFunc(),
			stringToTimeHookFunc(time.RFC3339),
		),
	})
	if err != nil {
		return fmt.Errorf(i18n.G("Error creating decoder: %v"), err)
	}

	err = decoder.Decode(data)
	if err != nil {
		return fmt.Errorf(i18n.G("Error decoding data: %v"), err)
	}

	return nil
}
