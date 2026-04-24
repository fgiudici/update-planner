GOFLAGS := -trimpath
DATE    := $(shell date +%y%m%d)

.PHONY: build
build: plcc2fbc

.PHONY: plcc2fbc
plcc2fbc:
	go build $(GOFLAGS) -o bin/plcc2fbc 

.PHONY: generate-fbc
generate-fbc: plcc2fbc
	bin/plcc2fbc --output fbc-samples/fbc-$(DATE).yaml
	cp -f fbc-samples/fbc-$(DATE).yaml fbc-samples/fbc-latest.yaml