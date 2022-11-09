# CLI Shorthand v1

?> This page describes the shorthand version 1 used by Restish 0.14.0 and older.

Restish comes with an optional contextual shorthand syntax for passing structured data into calls that require a body (i.e. `POST`, `PUT`, `PATCH`). While you can always pass full JSON or other documents through `stdin`, you can also specify or modify them by hand as arguments to the command using this shorthand syntax.

?> Note: for the examples below, you may need to escape or quote the values depending on your shell & settings. Instead of `foo.bar[].baz: 1`, use `'foo.bar[].baz: 1'`. If using `zsh` you can prefix a command with `noglob` to ignore `?` and `[]`.

For example:

```bash
# Make an HTTP POST with a JSON body
$ restish post api.rest.sh foo.bar[].baz: 1, .hello: world
```

Would result in the following body contents being sent on the wire (assuming a JSON content type):

```json
{
  "foo": {
    "bar": [
      {
        "baz": 1,
        "hello": "world"
      }
    ]
  }
}
```

The shorthand syntax supports the following features, described in more detail with examples below:

- Automatic type coercion & forced strings
- Nested object creation
- Object property grouping
- Nested array creation
- Appending to arrays
- Both object and array backreferences
- Loading property values from files
  - Supports structured, forced string, and base64 data

## Alternatives & Inspiration

The built-in CLI shorthand syntax is not the only one you can use to generate data for CLI commands. Here are some alternatives:

- [jo](https://github.com/jpmens/jo)
- [jarg](https://github.com/jdp/jarg)

For example, the shorthand example given above could be rewritten as:

```bash
$ jo -p foo=$(jo -p bar=$(jo -a $(jo baz=1 hello=world))) | restish post api.rest.sh
```

The built-in shorthand syntax implementation described herein uses those and the following for inspiration:

- [YAML](http://yaml.org/)
- [W3C HTML JSON Forms](https://www.w3.org/TR/html-json-forms/)
- [jq](https://stedolan.github.io/jq/)
- [JMESPath](http://jmespath.org/)

It seems reasonable to ask, why create a new syntax?

1. Built-in. No extra executables required.
2. No need to use sub-shells to build complex structured data.
3. Syntax is closer to YAML & JSON and mimics how we do queries using tools like `jq` and `jmespath`.

## Features in Depth

You can use the `j` executable from the [CLI Shorthand](https://github.com/danielgtaylor/shorthand) project to try out the shorthand format examples below. Examples are shown in JSON, but the shorthand parses into structured data that can be marshalled as other formats, like YAML or CBOR if you prefer.

```bash
$ go install github.com/danielgtaylor/shorthand/cmd/j@latest
```

Also feel free to use this tool to generate structured data for input to other commands.

### Keys & Values

At its most basic, a structure is built out of key & value pairs. They are separated by commas:

```bash
$ j hello: world, question: how are you?
{
  "hello": "world",
  "question": "how are you?"
}
```

### Types and Type Coercion

Well-known values like `null`, `true`, and `false` get converted to their respective types automatically. Numbers also get converted. Similar to YAML, anything that doesn't fit one of those is treated as a string. If needed, you can disable this automatic coercion by forcing a value to be treated as a string with the `~` operator. **Note**: the `~` modifier must come _directly after_ the colon.

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
$ j empty:~ null, bool:~ true, num:~ 1.5, string:~ hello
{
  "bool": "true",
  "empty": "null",
  "num": "1.5",
  "string": "hello"
}

# Passing the empty string
$ j blank:~
{
  "blank": ""
}

# Passing a tilde using whitespace
$ j foo: ~/Documents
{
  "foo": "~/Documents"
}

# Passing a tilde using forced strings
$ j foo:~~/Documents
{
  "foo": "~/Documents"
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

Properties of nested objects can be grouped by placing them inside `{` and `}`.

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

Simple arrays use a `,` between values. Nested arrays use square brackets `[` and `]` to specify the zero-based index to insert an item. Use a blank index to append to the array.

```bash
# Array shorthand
$ j a: 1, 2, 3
{
  "a": [
    1,
    2,
    3
  ]
}

# Nested arrays
$ j a[0][2][0]: 1
{
  "a": [
    [
      null,
      null,
      [
        1
      ]
    ]
  ]
}

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

### Backreferences

Since the shorthand syntax is context-aware, it is possible to use the current context to reference back to the most recently used object or array when creating new properties or items.

```bash
# Backref with object properties
$ j foo.bar: 1, .baz: 2
{
  "foo": {
    "bar": 1,
    "baz": 2
  }
}

# Backref with array appending
$ j foo.bar[]: 1, []: 2, []: 3
{
  "foo": {
    "bar": [
      1,
      2,
      3
    ]
  }
}

# Easily build complex structures
$ j name: foo, tags[]{id: 1, count.clicks: 5, .sales: 1}, []{id: 2, count.clicks: 8, .sales: 2}
{
  "name": "foo",
  "tags": [
    {
      "count": {
        "clicks": 5,
        "sales": 1
      },
      "id": 1
    },
    {
      "count": {
        "clicks": 8,
        "sales": 2
      },
      "id": 2
    }
  ]
}
```

### Loading from Files

Sometimes a field makes more sense to load from a file than to be specified on the commandline. The `@` preprocessor and `~` & `%` modifiers let you load structured data, strings, and base64-encoded data into values.

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

# Force loading a string
$ j foo: @~hello.json
{
  "foo": "{\n  \"hello\": \"world\"\n}"
}

# Load as base 64 data
$ j foo: @%hello.json
{
  "foo": "ewogICJoZWxsbyI6ICJ3b3JsZCIKfQ=="
}
```

Remember, it's possible to disable this behavior with the string modifier `~`:

```bash
$ j twitter:~ @user
{
  "twitter": "@user"
}
```
