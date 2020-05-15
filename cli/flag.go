package cli

import (
	"fmt"

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
		} else {
			flags.BoolP(name, short, viper.GetBool(name), description)
		}
	case int, int16, int32, int64, uint16, uint32, uint64:
		if multi {
			flags.IntSliceP(name, short, viper.Get(name).([]int), description)
		} else {
			flags.IntP(name, short, viper.GetInt(name), description)
		}
	case float32, float64:
		if multi {
			panic(fmt.Errorf("unshpported float slice param"))
		} else {
			flags.Float64P(name, short, viper.GetFloat64(name), description)
		}
	default:
		if multi {
			flags.StringSliceP(name, short, viper.Get(name).([]string), description)
		} else {
			flags.StringP(name, short, fmt.Sprintf("%v", viper.Get(name)), description)
		}
	}

	viper.BindPFlag(name, flags.Lookup(name))
}
