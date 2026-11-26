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
| `GET` | `/api/v1/info/PATH` | name, size, time and tag of one entry |
| `PUT` | `/api/v1/files/PATH` | upload, creating folders as needed; answers the new entry |
| `DELETE` | `/api/v1/files/PATH` | delete a file or a folder |
| `POST` | `/api/v1/folders/PATH` | create a folder |
| `POST` | `/api/v1/move` | `{"from": "...", "to": "..."}` |
| `POST` | `/api/v1/copy` | `{"from": "...", "to": "..."}`; a taken name becomes `name (2)` |
| `GET` | `/api/v1/preview/PATH` | an image or a text file up to 1 MB, shown inline |
| `GET` | `/api/v1/search?q=TEXT` | files and folders whose name contains the text |
| `GET` | `/api/v1/recent` | the files changed most recently |
| `GET` | `/api/v1/stars` | starred files and folders |
| `PUT`, `DELETE` | `/api/v1/stars/PATH` | star or unstar |
| `GET` | `/api/v1/trash` | items in the trash and how many days they are kept |
| `POST` | `/api/v1/trash/PATH` | move to the trash |
| `POST` | `/api/v1/restore/ID` | put an item back where it was |
| `DELETE` | `/api/v1/trash/ID` | delete one item for good |
| `DELETE` | `/api/v1/trash` | empty the trash |
| `GET` | `/api/v1/links` | the shared links of the user |
| `POST` | `/api/v1/links` | `{"path": "...", "days": 7}` makes a download link; `0` days never expires |
| `DELETE` | `/api/v1/links/TOKEN` | remove a link |
| `GET` | `/s/TOKEN` | download through a link, without signing in |

Every file entry carries an `etag`, also sent as the `ETag` header of a
download. A `PUT` with `If-Match: TAG` replaces the file only if it still
has that tag, so two devices editing the same file cannot overwrite each
other.

Items in the trash still count towards the quota and are deleted after 30
days. A file in the trash cannot be downloaded through a link; restoring it
brings back its links and stars.

Errors are `{"error": "..."}`: `sign_in` (401), `not_found` (404),
`invalid_name` (400), `not_a_file` (400), `exists` (409), `expired` (410),
`changed` (412), `no_preview` (415), `full` (507).

## Running

At the identity provider:

```sh
sinty-identity client add cloud -name "Sinty Cloud" -redirect https://cloud.example.org/cb -min-level 2 -confidential > client.secret
sinty-identity client add cloud-cli -name "Sinty Cloud on the command line" -redirect http://127.0.0.1/cb -min-level 2
sinty-identity client add desktop -name "Sinty OS" -redirect http://127.0.0.1/ -min-level 2 -offline
```

Then:

```sh
sinty-cloud serve -issuer https://id.example.org -public https://cloud.example.org \
  -client cloud -secret-file client.secret -accept cloud-cli,desktop -cert tls.crt -key tls.key
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
