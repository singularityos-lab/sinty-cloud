PREFIX ?= /usr

.PHONY: all build release test vet install clean

all: build

build:
	go build -o sinty-cloud ./cmd/sinty-cloud

release:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o sinty-cloud ./cmd/sinty-cloud

test:
	go test ./...

vet:
	go vet ./...

install: release
	install -Dm755 sinty-cloud $(DESTDIR)$(PREFIX)/bin/sinty-cloud
	install -Dm644 packaging/sinty-cloud.service $(DESTDIR)$(PREFIX)/lib/systemd/system/sinty-cloud.service
	install -Dm644 packaging/sinty-cloud.conf $(DESTDIR)/etc/default/sinty-cloud

clean:
	rm -f sinty-cloud
