PREFIX ?= $(HOME)/bin
VERSION ?= 0.1.0

build:
	go build -ldflags "-X hog/cmd.Version=$(VERSION)" -o hog .

install: build
	cp hog $(PREFIX)/hog
	codesign --force --sign - $(PREFIX)/hog

uninstall:
	rm -f $(PREFIX)/hog

clean:
	rm -f hog

.PHONY: build install uninstall clean
