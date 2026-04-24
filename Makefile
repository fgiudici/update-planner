GOFLAGS := -trimpath

.PHONY: build
build: plcc2fbc

plcc2fbc:
	go build $(GOFLAGS) -o bin/plcc2fbc 
