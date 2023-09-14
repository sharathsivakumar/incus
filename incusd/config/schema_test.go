package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/lxc/incus/incusd/config"
)

func TestSchema_Defaults(t *testing.T) {
	schema := config.Schema{
		"foo": {},
		"bar": {Default: "x"},
	}

	values := map[string]string{"foo": "", "bar": "x"}
	assert.Equal(t, values, schema.Defaults())
}

func TestSchema_Keys(t *testing.T) {
	schema := config.Schema{
		"foo": {},
		"bar": {Default: "x"},
	}

	keys := []string{"bar", "foo"}
	assert.Equal(t, keys, schema.Keys())
}
