# Cozy Garden
_a place to grow ideas_

Cozy Garden is a space to share and participate in creative challenges, ask intriguing questions, and meet likeminded learners. The platform is powered by a modified version of [Cerca](https://github.com/cblgh/cerca), a forum software built for the [Merveilles community forums](https://forum.merveilles.town)

## Local development

Install [golang](https://go.dev/).

To launch a local instance of the forum, run those commands (linux):

- `touch temp.txt`
- `mkdir data`
- `go run run.go --authkey 0 --allowlist temp.txt --dev`

It should respond `Serving forum on :8277`. Just go on [http://localhost:8272](http://localhost:8272).
