package cli

import (
	"github.com/alecthomas/chroma"
	"github.com/alecthomas/chroma/lexers"
)

// ReadableLexer colorizes the output of the Readable marshaller.
var ReadableLexer = lexers.Register(chroma.MustNewLexer(
	&chroma.Config{
		Name:         "CLI Readable",
		Aliases:      []string{"readable"},
		NotMultiline: true,
		DotAll:       true,
	},
	chroma.Rules{
		"whitespace": {
			{`\s+`, chroma.Text, nil},
		},
		"scalar": {
			{`(true|false|null)\b`, chroma.KeywordConstant, nil},
			{`"?0x[0-9a-f]+(...)?"?`, chroma.LiteralNumberHex, nil},
			{`"?[0-9]{4}-[0-9]{2}-[0-9]{2}(T[0-9:+-.]+Z?)?"?`, chroma.LiteralDate, nil},
			{`-?(0|[1-9]\d*)(\.\d+[eE](\+|-)?\d+|[eE](\+|-)?\d+|\.\d+)`, chroma.LiteralNumberFloat, nil},
			{`-?(0|[1-9]\d*)`, chroma.LiteralNumberInteger, nil},
			{`"([a-z]+://|/)(\\\\|\\"|[^"])+"`, chroma.LiteralStringSymbol, nil},
			{`"(\\\\|\\"|[^"])*"`, chroma.LiteralStringDouble, nil},
		},
		"objectrow": {
			{`:`, chroma.Punctuation, nil},
			{`\n`, chroma.Punctuation, chroma.Pop(1)},
			{`\}`, chroma.Punctuation, chroma.Pop(2)},
			chroma.Include("value"),
		},
		"object": {
			chroma.Include("whitespace"),
			{`\}`, chroma.Punctuation, chroma.Pop(1)},
			{`(\\\\|\\:|[^:])+`, chroma.NameTag, chroma.Push("objectrow")},
		},
		"arrayvalue": {
			{`\]`, chroma.Punctuation, chroma.Pop(1)},
			chroma.Include("value"),
		},
		"value": {
			chroma.Include("whitespace"),
			{`\{`, chroma.Punctuation, chroma.Push("object")},
			{`\[`, chroma.Punctuation, chroma.Push("arrayvalue")},
			chroma.Include("scalar"),
		},
		"root": {
			chroma.Include("value"),
		},
	},
))

// SchemaLexer colorizes schema output.
var SchemaLexer = lexers.Register(chroma.MustNewLexer(
	&chroma.Config{
		Name:         "CLI Schema",
		Aliases:      []string{"schema"},
		NotMultiline: true,
		DotAll:       true,
	},
	chroma.Rules{
		"whitespace": {
			{`\s+`, chroma.Text, nil},
		},
		"value": {
			chroma.Include("whitespace"),
			{`(\()([^ )]+)`, chroma.ByGroups(chroma.Text, chroma.Keyword), nil},
			{`([^:]+)(:)([^ )]+)`, chroma.ByGroups(chroma.String, chroma.Text, chroma.Text), nil},
			{`[^\n]*`, chroma.Text, chroma.Pop(1)},
		},
		"row": {
			chroma.Include("whitespace"),
			{`([^*:\n]+)(\*?)(:)`, chroma.ByGroups(chroma.NameTag, chroma.GenericStrong, chroma.Text), chroma.Push("value")},
			{`(\()([^ )]+)`, chroma.ByGroups(chroma.Text, chroma.Keyword), chroma.Push("value")},
		},
		"root": {
			chroma.Include("row"),
		},
	},
))
