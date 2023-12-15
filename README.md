# Cozy Garden
_a place to grow ideas_

Cozy Garden is a personal forum powered by a modified version of [Cerca](https://github.com/cblgh/cerca), a forum software built for the [Merveilles community forums](https://forum.merveilles.town)

## Local development

Install [golang](https://go.dev/).

To launch a local instance of the forum, run those commands (linux):

- `mkdir data`
- `go run run.go --authkey 0 --dev`

It should respond `Serving forum on :8277`. Just go on [http://localhost:8272](http://localhost:8272).
