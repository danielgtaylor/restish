package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/amzn/ion-go/ion"
	"github.com/danielgtaylor/restish/cli"
	"github.com/danielgtaylor/restish/openapi"
	"github.com/fxamacker/cbor/v2"
	"github.com/shamaton/msgpack/v2"
	"github.com/spf13/cobra"
)

func BenchmarkFormats(b *testing.B) {
	inputs := []struct {
		Name string
		URL  string
	}{
		{
			Name: "small",
			URL:  "https://github.com/OAI/OpenAPI-Specification/raw/d1cc440056f1c7bb913bcd643b15c14ee1c409f4/examples/v3.0/uspto.json",
		},
		{
			Name: "large",
			URL:  "https://github.com/github/rest-api-description/blob/83cdec7384b62ef6f54bad60270544d6fc6f22cd/descriptions/api.github.com/api.github.com.json?raw=true",
		},
	}

	cli.Init("benchmark", "1.0.0")
	cli.Defaults()
	cli.AddLoader(openapi.New())

	for _, t := range inputs {
		resp, err := http.Get(t.URL)
		if err != nil {
			panic(err)
		}
		if resp.StatusCode >= 300 {
			panic("non-success status from server, check URLs are still working")
		}

		dummy := &cobra.Command{}
		doc, err := cli.Load(t.URL, dummy)
		if err != nil {
			panic(err)
		}

		dataJSON, err := json.Marshal(doc)
		if err != nil {
			panic(err)
		}

		dataCBOR, err := cbor.Marshal(doc)
		if err != nil {
			panic(err)
		}

		dataMsgPack, err := msgpack.Marshal(doc)
		if err != nil {
			panic(err)
		}

		dataIon, err := ion.MarshalBinary(doc)
		if err != nil {
			panic(err)
		}

		fmt.Printf("json: %d\ncbor: %d\nmsgp: %d\n ion: %d\n", len(dataJSON), len(dataCBOR), len(dataMsgPack), len(dataIon))

		b.Run(t.Name+"-json-marshal", func(b *testing.B) {
			b.ReportAllocs()
			for n := 0; n < b.N; n++ {
				json.Marshal(doc)
			}
		})

		b.Run(t.Name+"-json-unmarshal", func(b *testing.B) {
			b.ReportAllocs()
			for n := 0; n < b.N; n++ {
				var tmp cli.API
				json.Unmarshal(dataJSON, &tmp)
			}
		})

		b.Run(t.Name+"-cbor-marshal", func(b *testing.B) {
			b.ReportAllocs()
			for n := 0; n < b.N; n++ {
				cbor.Marshal(doc)
			}
		})

		b.Run(t.Name+"-cbor-unmarshal", func(b *testing.B) {
			b.ReportAllocs()
			for n := 0; n < b.N; n++ {
				var tmp cli.API
				cbor.Unmarshal(dataCBOR, &tmp)
			}
		})

		b.Run(t.Name+"-msgpack-marshal", func(b *testing.B) {
			b.ReportAllocs()
			for n := 0; n < b.N; n++ {
				msgpack.Marshal(doc)
			}
		})

		b.Run(t.Name+"-msgpack-unmarshal", func(b *testing.B) {
			b.ReportAllocs()
			for n := 0; n < b.N; n++ {
				var tmp cli.API
				msgpack.Unmarshal(dataMsgPack, &tmp)
			}
		})

		b.Run(t.Name+"-ion-marshal", func(b *testing.B) {
			b.ReportAllocs()
			for n := 0; n < b.N; n++ {
				ion.MarshalBinary(doc)
			}
		})

		b.Run(t.Name+"-ion-unmarshal", func(b *testing.B) {
			b.ReportAllocs()
			for n := 0; n < b.N; n++ {
				var tmp cli.API
				ion.Unmarshal(dataIon, &tmp)
			}
		})
	}
}
