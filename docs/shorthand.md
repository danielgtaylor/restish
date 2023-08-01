# CLI Shorthand

?> This describes CLI Shorthand version 2. Restish 0.14.0 and older use [Shorthand v1](shorthandv1.md) instead.

The CLI Shorthand language is a superset of JSON that is made in part to support passing structured data easily on the command line for the bodies of `POST`, `PUT`, `PATCH`, etc API operations. Some of its high level features include:

- Superset of JSON (valid JSON is valid shorthand)
  - Optional commas, quotes, and sometimes colons
- Addtional types & binary
  - Support for bytes, datetimes, and maps with non-string keys
- Easy nested object & array creation
- Basic templating
  - Mix stdin with passed arguments
  - Load values from files
- Editing existing data
  - Appending & inserting to arrays
  - Unsetting properties
  - Moving properties & items

Here is a diagram overview of the language syntax, which is similar to [JSON's syntax](https://www.json.org/json-en.html) but adds a few things:

<!--
https://tabatkins.github.io/railroad-diagrams/generator.html

Diagram(
    Choice(0,
      Sequence('//', NonTerminal('comment')),
      Sequence(
        '{',
        OneOrMore(
          Sequence(
            OneOrMore(Sequence(NonTerminal('string'), Optional(Sequence('[', Optional('^'), ZeroOrMore('0-9'), ']'))), '.'),
            Choice(0,
              Sequence(':', NonTerminal('value')),
              Sequence('^', NonTerminal('query')),
              NonTerminal('object'),
            ),
          ),
          Choice(0, ',', '\\n'),
        ),
        '}'
      ),
      Sequence(
        '[',
        OneOrMore(NonTerminal('value'), Choice(0, ',', '\\n')),
        ']'
      ),
      'undefined',
      'null',
      'true',
      'false',
      NonTerminal('integer'),
      NonTerminal('float'),
      Stack(
        Sequence(
          NonTerminal('YYYY'),
          '-',
          NonTerminal('MM'),
          '-',
          NonTerminal('DD'),
        ),
          Sequence(
          'T',
          NonTerminal('hh'),
          ':',
          NonTerminal('mm'),
          ':',
          NonTerminal('ss'),
          NonTerminal('zone')
        ),
      ),
      Sequence('%', NonTerminal('base64')),
      Sequence('@', NonTerminal('filename')),
      NonTerminal('string'),
    ),
)
-->

![shorthand-syntax](https://user-images.githubusercontent.com/106826/198850895-a1a8481a-2c63-484c-9bf2-ce472effa8c3.svg)

Note:

- `string` can be quoted (with `"`) or unquoted.
- The `query` syntax in the diagram above is described below in the [Querying](#querying) section.

## Alternatives & inspiration

The CLI shorthand syntax is not the only one you can use to generate data for CLI commands. Here are some alternatives:

- [jo](https://github.com/jpmens/jo)
- [jarg](https://github.com/jdp/jarg)

For example, the shorthand example given above could be rewritten as:

```bash
$ jo -p foo=$(jo -p bar=$(jo -a $(jo -p baz=1 hello=world)))
```

The shorthand syntax implementation described herein uses those and the following for inspiration:

- [YAML](http://yaml.org/)
- [W3C HTML JSON Forms](https://www.w3.org/TR/html-json-forms/)
- [jq](https://stedolan.github.io/jq/)
- [JMESPath](http://jmespath.org/)

It seems reasonable to ask, why create a new syntax?

1. Built-in. No extra executables required. Your tool ships ready-to-go.
2. No need to use sub-shells to build complex structured data.
3. Syntax is closer to YAML & JSON and mimics how you do queries using tools like `jq` and `jmespath`.
4. It's _optional_, so you can use your favorite tool/language instead, while at the same time it provides a minimum feature set everyone will have in common.

## Features in depth

You can use the included `j` executable to try out the shorthand format examples below. Examples are shown in JSON, but the shorthand parses into structured data that can be marshalled as other formats, like YAML or TOML if you prefer.

```bash
go get -u github.com/danielgtaylor/shorthand/cmd/j
```

Also feel free to use this tool to generate structured data for input to other commands.

?> Note: for the examples below, you may need to escape or quote the values depending on your shell & settings. Instead of `foo.bar[].baz: 1`, use `'foo.bar[].baz: 1'`. If using `zsh` you can prefix a command with `noglob` to ignore `?` and `[]`.

### Keys & values

At its most basic, a structure is built out of key & value pairs. They are separated by commas:

```bash
$ j hello: world, question: how are you?
{
  "hello": "world",
  "question": "how are you?"
}
```

### Types

Shorthand supports the standard JSON types, but adds some of its own as well to better support binary formats and its query features.

| Type      | Description                                                      |
| --------- | ---------------------------------------------------------------- |
| `null`    | JSON `null`                                                      |
| `boolean` | Either `true` or `false`                                         |
| `number`  | JSON number, e.g. `1`, `2.5`, or `1.4e5`                         |
| `string`  | Quoted or unquoted strings, e.g. `hello` or `"hello"`            |
| `bytes`   | `%`-prefixed, unquoted, base64-encoded binary data, e.g. `%wg==` |
| `time`    | Date/time in ISO8601, e.g. `2022-01-01T12:00:00Z`                |
| `array`   | JSON array, e.g. `[1, 2, 3]`                                     |
| `object`  | JSON object, e.g. `{"hello": "world"}`                           |

### Type coercion

Well-known values like `null`, `true`, and `false` get converted to their respective types automatically. Numbers, bytes, and times also get converted. Similar to YAML, anything that doesn't fit one of those is treated as a string. This automatic coercion can be disabled by just wrapping your value in quotes.

```bash
# With coercion
$ j empty: null, bool: true, num: 1.5, string: hello
{
  "bool": true,
  "empty": null,
  "num": 1.5,
  "string": "hello"
}

# As strings
$ j empty: "null", bool: "true", num: "1.5", string: "hello"
{
  "bool": "true",
  "empty": "null",
  "num": "1.5",
  "string": "hello"
}

# Passing the empty string
$ j blank1: , blank2: ""
{
  "blank1": "",
  "blank2": ""
}
```

### Objects

Nested objects use a `.` separator when specifying the key.

```bash
$ j foo.bar.baz: 1
{
  "foo": {
    "bar": {
      "baz": 1
    }
  }
}
```

Properties of nested objects can be grouped by placing them inside `{` and `}`. The `:` becomes optional for nested objects, so `foo.bar: {...}` is equivalent to `foo.bar{...}`.

```bash
$ j foo.bar{id: 1, count.clicks: 5}
{
  "foo": {
    "bar": {
      "count": {
        "clicks": 5
      },
      "id": 1
    }
  }
}
```

### Arrays

Arrays are surrounded by square brackets like in JSON:

```bash
# Simple array
$ j [1, 2, 3]
[
  1,
  2,
  3
]
```

Array indexes use square brackets `[` and `]` to specify the zero-based index to set an item. If the index is out of bounds then `null` values are added as necessary to fill the array. Use an empty index `[]` to append to the an existing array. If the item is not an array, then a new one will be created.

```bash
# Nested arrays
$ j [0][2][0]: 1
[
  [
    null,
    null,
    [
      1
    ]
  ]
]

# Appending arrays
$ j a[]: 1, a[]: 2, a[]: 3
{
  "a": [
    1,
    2,
    3
  ]
}
```

### Loading from files

Sometimes a field makes more sense to load from a file than to be specified on the commandline. The `@` preprocessor lets you load structured data, text, and bytes depending on the file extension and whether all bytes are valid UTF-8:

```bash
# Load a file's value as a parameter
$ j foo: @hello.txt
{
  "foo": "hello, world"
}

# Load structured data
$ j foo: @hello.json
{
  "foo": {
    "hello": "world"
  }
}
```

Remember, it's possible to disable this behavior with quotes:

```bash
$ j 'twitter: "@user"'
{
  "twitter": "@user"
}
```

### Patch (partial update)

Partial updates are supported on existing data, which can be used to implement HTTP `PATCH`, templating, and other similar features. The suggested content type for HTTP `PATCH` is `application/merge-patch+shorthand`. This feature combines the best of both:

- [JSON Merge Patch](https://datatracker.ietf.org/doc/html/rfc7386)
- [JSON Patch](https://www.rfc-editor.org/rfc/rfc6902)

Partial updates support:

- Appending arrays via `[]`
- Inserting before via `[^index]`
- Removing fields or array items via `undefined`
- Moving/swapping fields or array items via `^`
  - The right hand side is a path to the value to swap. See Querying below for the path syntax.

Note: When sending shorthand patches file loading via `@` should be disabled as the files will not exist on the server.

Some examples:

```bash
# First, let's create some data we'll modify later
$ j id: 1, tags: [a, b, c] >data.json

# Now let's append to the tags array
$ j <data.json 'tags[]: d'
{
  "id": 1,
  "tags": [
    "a",
    "b",
    "c",
    "d"
  ]
}

# Array item insertion (prepend the array)
$ j <data.json 'tags[^0]: z'
{
  "id": 1,
  "tags": [
    "z",
    "a",
    "b",
    "c"
  ]
}

# Remove stuff
$ j <data.json 'id: undefined, tags[1]: undefined'
{
  "tags": [
    "a",
    "c"
  ]
}

# Rename the ID property, and swap the first/last array items
$ j <data.json 'id ^ name, tags[0] ^ tags[-1]'
{
  "name": 1,
  "tags": [
    "c",
    "b",
    "a"
  ]
}
```

## Querying

A data query language is included, which allows you to query, filter, and select fields to return. This functionality is used by the patch move operations described above and is similar to tools like:

- [jq](https://stedolan.github.io/jq/)
- [JMESPath](http://jmespath.org/)
- [JSON Path](https://www.ietf.org/archive/id/draft-ietf-jsonpath-base-06.html)

The query language supports:

- Paths for objects & arrays `foo.items.name`
- Wildcards for unknown props `foo.*.name`
- Array indexing & slicing `foo.items[1:2].name`
  - Including negative indexes `foo.items[-1].name`
- Array filtering via [mexpr](https://github.com/danielgtaylor/mexpr) `foo.items[name.lower startsWith d]`
- Object property selection `foo.{created, names: items.name}`
- Recursive search `foo..name`
- Stopping processing with a pipe `|`
- Flattening nested arrays `[]`

The query syntax is recursive and looks like this:

<!--
Diagram(
  Stack(
    OneOrMore(Sequence(
      Choice(1,
        Skip(),
        NonTerminal('string'),
        '*',
      ),
      Optional(
        Sequence(
          '[',
          Choice(1,
            Skip(),
            NonTerminal('number'),
            NonTerminal('slice'),
            NonTerminal('filter')
          ),
          ']',
        )
      ),
      Optional('|'),
    ), Choice(0, '.', '..')),
    Optional(
      Sequence(
        '.',
        '{',
        OneOrMore(
          Sequence(
            NonTerminal('string'),
            Optional(
              Sequence(':', NonTerminal('query')),
              'skip'
            ),
          ),
          ','
        ),
        '}',
      ), 'skip',
    ),
  )
)
-->

![shorthand-query-syntax](https://user-images.githubusercontent.com/106826/198693468-fadf8d48-8223-4dd9-a2cb-a1651e342fc5.svg)

The `filter` syntax is described in the documentation for [mexpr](https://github.com/danielgtaylor/mexpr).

Examples:

```bash
# First, let's make a complex file to query
$ j 'users: [{id: 1, age: 5, friends: [a, b]}, {id: 2, age: 6, friends: [b, c]}, {id: 3, age: 5, friends: [c, d]}]' >data.json

# Query for each user's ID
$ j <data.json -q 'users.id'
[
  1,
  2,
  3
]

# Get the users who are friends with `b`
$ j <data.json -q 'users[friends contains b].id'
[
  1,
  2
]

# Get the ID & age of users who are friends with `b`
$ j <data.json -q 'users[friends contains b].{id, age}'
[
  {
    "age": null,
    "id": 1
  },
  {
    "age": null,
    "id": 2
  }
]
```
