# sinty-cloud

File storage with web access for people and organisations. It has no login of
its own: people sign in through [sinty-identity](https://github.com/singularityos-lab/sinty-identity),
and the service decides how strong that sign-in must be.

## What it does

- Each person has a private space under `-data`, opened with `os.Root`, so no
  path, `..` or symbolic link reaches outside it.
- Uploads are written to a temporary file and renamed into place; a space
  never holds half a file and never goes past its quota.
- Downloads are always attachments with `Content-Security-Policy: sandbox`,
  so a stored HTML file cannot run as the cloud.
- The web app is built on [sintyui](https://github.com/singularityos-lab/sintyui).
- The same API serves the web app (session cookie plus the
  `X-Sinty-Request` header) and the command line (bearer token).

## API

| Method | Path | |
| --- | --- | --- |
| `GET` | `/api/v1/me` | user, bytes used, quota |
| `GET` | `/api/v1/files/PATH` | folder listing as JSON, or the file |
| `PUT` | `/api/v1/files/PATH` | upload, creating folders as needed |
| `DELETE` | `/api/v1/files/PATH` | delete a file or a folder |
| `POST` | `/api/v1/folders/PATH` | create a folder |
| `POST` | `/api/v1/move` | `{"from": "...", "to": "..."}` |

Errors are `{"error": "..."}`: `sign_in` (401), `not_found` (404),
`invalid_name` (400), `exists` (409), `full` (507).

## Running

At the identity provider:

```sh
sinty-identity client add cloud -name "Sinty Cloud" -redirect https://cloud.example.org/cb -min-level 2 -confidential > client.secret
sinty-identity client add cloud-cli -name "Sinty Cloud on the command line" -redirect http://127.0.0.1/cb -min-level 2
```

Then:

```sh
sinty-cloud serve -issuer https://id.example.org -public https://cloud.example.org \
  -client cloud -secret-file client.secret -cli-client cloud-cli -cert tls.crt -key tls.key
```

## Command line

```sh
sinty-cloud login -server https://cloud.example.org -issuer https://id.example.org
sinty-cloud put report.pdf docs/report.pdf
sinty-cloud ls docs
sinty-cloud get docs/report.pdf
sinty-cloud rm docs/report.pdf
sinty-cloud usage
```

`login` opens the sign-in page in the browser and keeps the token in
`$XDG_CONFIG_HOME/sinty-cloud/credentials.json` (mode 0600). Output is JSON.
Exit status: 0 done, 1 failed, 2 wrong usage, 3 signed out, 4 not found,
5 space full.

## Licence

GPL-3.0, see LICENSE.
