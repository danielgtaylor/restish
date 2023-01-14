package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

// AddGlobalFlag will make a new global flag on the root command.
func AddGlobalFlag(name, short, description string, defaultValue interface{}, multi bool) {
	viper.SetDefault(name, defaultValue)

	flags := Root.PersistentFlags()
	switch defaultValue.(type) {
	case bool:
		if multi {
			flags.BoolSliceP(name, short, viper.Get(name).([]bool), description)
			GlobalFlags.BoolSliceP(name, short, viper.Get(name).([]bool), description)
		} else {
			flags.BoolP(name, short, viper.GetBool(name), description)
			GlobalFlags.BoolP(name, short, viper.GetBool(name), description)
		}
	case int, int16, int32, int64, uint16, uint32, uint64:
		if multi {
			flags.IntSliceP(name, short, viper.Get(name).([]int), description)
			GlobalFlags.IntSliceP(name, short, viper.Get(name).([]int), description)
		} else {
			flags.IntP(name, short, viper.GetInt(name), description)
			GlobalFlags.IntP(name, short, viper.GetInt(name), description)
		}
	case float32, float64:
		if multi {
			panic(fmt.Errorf("unsupported float slice param"))
		} else {
			flags.Float64P(name, short, viper.GetFloat64(name), description)
			GlobalFlags.Float64P(name, short, viper.GetFloat64(name), description)
		}
	default:
		if multi {
			v := viper.Get(name)
			if s, ok := v.(string); ok {
				// Probably loaded from the environment.
				v = strings.Split(s, ",")
				viper.Set(name, v)
			}
			flags.StringArrayP(name, short, v.([]string), description)
			GlobalFlags.StringArrayP(name, short, v.([]string), description)
		} else {
			flags.StringP(name, short, fmt.Sprintf("%v", viper.Get(name)), description)
			GlobalFlags.StringP(name, short, fmt.Sprintf("%v", viper.Get(name)), description)
		}
	}

	viper.BindPFlag(name, flags.Lookup(name))
}
