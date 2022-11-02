package cli

import (
	"github.com/alecthomas/chroma"
	"github.com/alecthomas/chroma/lexers"
)

// ReadableLexer colorizes the output of the Readable marshaller.
var ReadableLexer = lexers.Register(chroma.MustNewLazyLexer(
	&chroma.Config{
		Name:         "CLI Readable",
		Aliases:      []string{"readable"},
		NotMultiline: true,
		DotAll:       true,
	},
	func() chroma.Rules {
		return chroma.Rules{
			"whitespace": {
				{
					Pattern: `\s+`,
					Type:    chroma.Text,
				},
			},
			"scalar": {
				{
					Pattern: `(true|false|null)\b`,
					Type:    chroma.KeywordConstant,
				},
				{
					Pattern: `"?0x[0-9a-f]+(\\.\\.\\.)?"?`,
					Type:    chroma.LiteralNumberHex,
				},
				{
					Pattern: `"?[0-9]{4}-[0-9]{2}-[0-9]{2}(T[0-9:+-.]+Z?)?"?`,
					Type:    chroma.LiteralDate,
				},
				{
					Pattern: `-?(0|[1-9]\d*)(\.\d+[eE](\+|-)?\d+|[eE](\+|-)?\d+|\.\d+)`,
					Type:    chroma.LiteralNumberFloat,
				},
				{
					Pattern: `-?(0|[1-9]\d*)`,
					Type:    chroma.LiteralNumberInteger,
				},
				{
					Pattern: `"([a-z]+://|/)(\\\\|\\"|[^"])+"`,
					Type:    chroma.LiteralStringSymbol,
				},
				{
					Pattern: `"(\\\\|\\"|[^"])*"`,
					Type:    chroma.LiteralStringDouble,
				},
			},
			"objectrow": {
				{
					Pattern: `:`,
					Type:    chroma.Punctuation,
				},
				{
					Pattern: `\n`,
					Type:    chroma.Punctuation,
					Mutator: chroma.Pop(1),
				},
				{
					Pattern: `\}`,
					Type:    chroma.Punctuation,
					Mutator: chroma.Pop(2),
				},
				chroma.Include("value"),
			},
			"object": {
				chroma.Include("whitespace"),
				{
					Pattern: `\}`,
					Type:    chroma.Punctuation,
					Mutator: chroma.Pop(1),
				},
				{
					Pattern: `(\\\\|\\:|[^:])+`,
					Type:    chroma.NameTag,
					Mutator: chroma.Push("objectrow"),
				},
			},
			"arrayvalue": {
				{
					Pattern: `\]`,
					Type:    chroma.Punctuation,
					Mutator: chroma.Pop(1),
				},
				chroma.Include("value"),
			},
			"value": {
				chroma.Include("whitespace"),
				{
					Pattern: `\{`,
					Type:    chroma.Punctuation,
					Mutator: chroma.Push("object"),
				},
				{
					Pattern: `\[`,
					Type:    chroma.Punctuation,
					Mutator: chroma.Push("arrayvalue"),
				},
				chroma.Include("scalar"),
			},
			"root": {
				chroma.Include("value"),
			},
		}
	},
))

// SchemaLexer colorizes schema output.
var SchemaLexer = lexers.Register(chroma.MustNewLazyLexer(
	&chroma.Config{
		Name:         "CLI Schema",
		Aliases:      []string{"schema"},
		NotMultiline: true,
		DotAll:       true,
	},
	func() chroma.Rules {
		return chroma.Rules{
			"whitespace": {
				{Pattern: `\s+`, Type: chroma.Text},
			},
			"value": {
				chroma.Include("whitespace"),
				{
					Pattern: `(\()([^ )]+)`,
					Type:    chroma.ByGroups(chroma.Text, chroma.Keyword),
				},
				{
					Pattern: `([^:]+)(:)([^ )]+)`,
					Type:    chroma.ByGroups(chroma.String, chroma.Text, chroma.Text),
				},
				{
					Pattern: `[^\n]*`, Type: chroma.Text, Mutator: chroma.Pop(1),
				},
			},
			"row": {
				chroma.Include("whitespace"),
				{
					Pattern: `([^*:\n]+)(\*?)(:)`,
					Type:    chroma.ByGroups(chroma.NameTag, chroma.GenericStrong, chroma.Text),
					Mutator: chroma.Push("value"),
				},
				{
					Pattern: `(\()([^ )]+)`,
					Type:    chroma.ByGroups(chroma.Text, chroma.Keyword),
					Mutator: chroma.Push("value"),
				},
			},
			"root": {
				chroma.Include("row"),
			},
		}
	},
))
