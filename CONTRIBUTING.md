# Contributing to sinty-cloud

Contributions are welcome under the terms in CLA.md.

```sh
git clone https://github.com/singularityos-lab/sinty-cloud
cd sinty-cloud
make test
```

- Every file operation must stay inside the signed-in user's space; add a test
  for any new way a path or a name can reach the disk.
- Run `gofmt` and `go vet ./...` before sending a change.
