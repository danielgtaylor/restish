# Bulk resource management

Restish includes support for a `git`-like client-side bulk resource management interface, enabling you to pull resources onto disk and track both remote and local changes, get diffs, batch submit updates, etc.

## Prerequisites

Any API that supports versioned resources and provides that version in the list response can work with Restish's bulk resource management.

If common response field names and standard conditional update headers are used, things should just work out of the box. If not, then there are arguments to massage the response data into a format Restish can understand, described in more detail in the reference below.

For example, you might have a simple CRUD-style API like this, which fully supports bulk operations:

```text
GET    /books           List books returns [{url: "...", version: "..."}]
GET    /books/{book-id} Get book
PUT    /books/{book-id} Create/update book
DELETE /books/{book-id} Delete book
```

?> Resource creation via Restish `bulk` requires the use of client-generated identifiers and HTTP `PUT` requests. Use plain old `restish POST ...` otherwise.

## Getting started & demo

Let's see how it works in action by using an example CRUD API:

```bash
# First, let's create a simple alias for running bulk commands.
$ alias rb="restish bulk"

# Initialize your first checkout
$ mkdir books && cd books
$ rb init api.rest.sh/books
```

You will see the book resources get downloaded and saved on disk. You can list all the resources, or filter them by matching specific criteria using [mexpr](https://github.com/danielgtaylor/mexpr) expressions, which show any resource whose expression result is "truthy" (meaning a non-zero scalar or non-empty map/slice).

```bash
# List all the resources
$ rb list
letters-from-an-astrophysicist.json
sapiens.json
the-demon-haunted-world.json
the-fabric-of-the-cosmos.json
the-food-lab.json

# List books with high average ratings
$ rb list --match='rating_average >= 4.8'
letters-from-an-astrophysicist.json
the-food-lab.json

# List books with recent ratings
$ rb list --match='recent_ratings where date after "2022-01-01"'
sapiens.json

# List books written by Brian
$ rb list --match='author.lower contains brian'
the-fabric-of-the-cosmos.json
```

Restish also understands JSON Schema, so if the resources advertise a schema (e.g. via a `describedby` link relation of a `$schema` property) then it can provide useful errors when filtering. Since the example books advertise a schema at <https://api.rest.sh/schemas/Book.json> we can get warnings about potential expression problems:

```bash
$ rb list --match='recent_ratings > 5'
WARN: cannot compare array with number
recent_ratings > 5
...............^^^^
```

Additionally, you can use the `-f` flag to apply a [Shorthand Query](./shorthand.md#querying) filter to each matched file and print out the result, enabling a quick way to get specific values from a set of matched files:

```bash
# Get the most recent rating of each matched book
$ rb list -m 'rating_average > 4.7' -f 'recent_ratings[0].rating'
letters-from-an-astrophysicist.json
4.9
the-food-lab.json
5
```

Next, let's make some changes!

```bash
# Delete a book locally!
$ rm the-food-lab.json

# Create a new book with minimal config
$ echo '{"title": "my-new-book"}' >my-new-book.json

# Get the status (note, the server updates sapiens frequently)
$ rb status
Remote changes on https://api.rest.sh/books
  (use "restish bulk pull" to update)
	modified:  sapiens.json
Local changes:
  (use "restish bulk reset [file]..." to undo)
  (use "restish bulk diff [file]..." to view changes)
	   added:  my-new-book.json
	 removed:  the-food-lab.json

# Reset the deletion so we don't delete the book on the server!
$ rb reset the-food-lab.json

# Push your changes
$ rb push
Pushing resources... 100% |████████████████████████████████████████|
Push complete.
```

The server now has a `my-new-book` resource. In the future, you can update to the latest changes that others make via:

```bash
# Pull the latest
$ rb pull
Pulling resources... 100% |████████████████████████████████████████|

# See no changes
$ rb status
You are up to date with https://api.rest.sh/books
No local changes
```

## Reference

### Init

```bash
restish bulk init URL [-f filter] [--url-template tmpl]
```

Initialize a new bulk checkout. The response should be a list of resources which contain a link URL and version, or optionally you can pass a filter or URL template to build the link URL and/or version needed to fetch listed resources.

Alias: `i`

| Param / Option       | Description & Example                                                                                                                                                          |
| -------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `URL`                | The URL to list resources<br/>Example: `api.rest.sh/books`                                                                                                                     |
| `-f`, `--rsh-filter` | Filter the response via [Shorthand Query](./shorthand.md#querying)<br/>Example: `-f 'body.{id, version: last_modified_dt}'`                                                    |
| `--url-template`     | Template string to build URLs from list response items. If a filter is passed, it is processed _before_ rendering the URL template.<br/>Example: `--url-template='/items/{id}` |

#### Automatically recognized fields

The following fields are automatically recognized and used when available in the list response items, allowing bulk resource management to just work out of the box with a large number of APIs. Fields are checked in the order listed below and the first that is found will be used.

| Description      | Field Names                                                    |
| ---------------- | -------------------------------------------------------------- |
| Resource URL     | `url`, `uri`, `self`, `link`                                   |
| Resource version | `version`, `etag`, `last_modified`, `lastModified`, `modified` |

#### Complex example

For a more complex example, let's assume you have an API at `example.com/items` which returns resources for multiple people via a list operation like this:

```json
{
	"items": [
		{
			"owner": "alice",
			"id": "abc123",
			"unique_hash": "a1b1"
		},
		{
			"owner": "bob",
			"id": "def456",
			"unique_hash": "a1b2"
		},
		...
	]
}
```

Each individual item is available under the owner's name, e.g. the first item would be fetched via `example.com/items/alice/abc123`. You can initialize this bulk checkout via Restish like so:

```bash
$ rb init example.com/items \
	-f 'body.items.{owner, id, version: unique_hash}' \
	--url-template='/items/{owner}/{id}'
```

### List

```bash
restish bulk list [--match expr] [-f filter]
```

List checked out resources, optionally with filtering via expressions.

Alias: `ls`

| Param / Option       | Description & Example                                                                                                                 |
| -------------------- | ------------------------------------------------------------------------------------------------------------------------------------- |
| `-m`, `--match`      | Match resources using [mexpr](https://github.com/danielgtaylor/mexpr) expressions<br/>Example: `-m 'rating_average >= 4.8'`           |
| `-f`, `--rsh-filter` | Filter each resource via [Shorthand Query](./shorthand.md#querying) and print the result<br/>Example: `-f 'recent_ratings[0].rating'` |

?> Match expressions show any resource whose expression result is "truthy" (meaning a non-zero scalar or non-empty map/slice). `false`, `0`, `""`, `[]`, and `{}` are considered "falsey".

### Status

```bash
restish bulk status
```

Show the local & remote added/changed/removed files.

Alias: `st`

### Diff

```bash
restish bulk diff [FILE... | --match expr | --remote]
```

Show a diff of local or remote changed files.

Alias: `di`

| Param / Option  | Description & Example                                                                                                       |
| --------------- | --------------------------------------------------------------------------------------------------------------------------- |
| `-m`, `--match` | Match resources using [mexpr](https://github.com/danielgtaylor/mexpr) expressions<br/>Example: `-m 'rating_average >= 4.8'` |
| `--remote`      | Show remote diffs instead of local                                                                                          |

?> Remote diffs can be useful to see changes before doing a `rb pull`!

### Reset

```bash
restish bulk reset [FILE... | --match expr]
```

Undo local changes to files.

Alias: `re`

| Param / Option  | Description & Example                                                                                                       |
| --------------- | --------------------------------------------------------------------------------------------------------------------------- |
| `-m`, `--match` | Match resources using [mexpr](https://github.com/danielgtaylor/mexpr) expressions<br/>Example: `-m 'rating_average >= 4.8'` |

### Pull

```bash
restish bulk pull
```

Pull remote updates. Use `restish bulk status` to see if there are remote updates to pull.

Pulling does not overwrite local changes. Use `restish bulk reset FILE` to overwrite local changes after a pull.

Alias: `pl`

### Push

```bash
restish bulk push
```

Upload local changes to the remote server. Resources are updated sequentially (one after the other).

Alias: `ps`
