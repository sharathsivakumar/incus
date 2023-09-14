GO ?= go
DOMAIN=incus
POFILES=$(wildcard po/*.po)
MOFILES=$(patsubst %.po,%.mo,$(POFILES))
LINGUAS=$(basename $(POFILES))
POTFILE=po/$(DOMAIN).pot
VERSION=$(shell grep "var Version" internal/version/flex.go | cut -d'"' -f2)
ARCHIVE=incus-$(VERSION).tar
HASH := \#
TAG_SQLITE3=$(shell printf "$(HASH)include <cowsql.h>\nvoid main(){cowsql_node_id n = 1;}" | $(CC) ${CGO_CFLAGS} -o /dev/null -xc - >/dev/null 2>&1 && echo "libsqlite3")
GOPATH ?= $(shell $(GO) env GOPATH)
CGO_LDFLAGS_ALLOW ?= (-Wl,-wrap,pthread_create)|(-Wl,-z,now)
SPHINXENV=doc/.sphinx/venv/bin/activate

ifneq "$(wildcard vendor)" ""
	RAFT_PATH=$(CURDIR)/vendor/raft
	COWSQL_PATH=$(CURDIR)/vendor/cowsql
else
	RAFT_PATH=$(GOPATH)/deps/raft
	COWSQL_PATH=$(GOPATH)/deps/cowsql
endif

	# raft
.PHONY: default
default: build

.PHONY: build
build:
ifeq "$(TAG_SQLITE3)" ""
	@echo "Missing cowsql, run \"make deps\" to setup."
	exit 1
endif

	CC="$(CC)" CGO_LDFLAGS_ALLOW="$(CGO_LDFLAGS_ALLOW)" $(GO) install -v -tags "$(TAG_SQLITE3)" $(DEBUG) ./...
	CGO_ENABLED=0 $(GO) install -v -tags netgo ./cmd/incus-migrate
	CGO_ENABLED=0 $(GO) install -v -tags agent,netgo ./cmd/incus-agent
	cd cmd/lxd-to-incus && CC="$(CC)" CGO_LDFLAGS_ALLOW="$(CGO_LDFLAGS_ALLOW)" $(GO) install -v ./
	@echo "Incus built successfully"

.PHONY: client
client:
	$(GO) install -v -tags "$(TAG_SQLITE3)" $(DEBUG) ./cmd/incus
	@echo "Incus client built successfully"

.PHONY: incus-agent
incus-agent:
	CGO_ENABLED=0 $(GO) install -v -tags agent,netgo ./cmd/incus-agent
	@echo "Incus agent built successfully"

.PHONY: incus-migrate
incus-migrate:
	CGO_ENABLED=0 $(GO) install -v -tags netgo ./cmd/incus-migrate
	@echo "Incus migration tool built successfully"

.PHONY: incus-doc
incus-doc:
	@$(GO) version > /dev/null 2>&1 || { echo "go is not installed for incus-doc installation."; exit 1; }
	cd incusd/config/generate && CGO_ENABLED=0 $(GO) build -o $(GOPATH)/bin/incus-doc
	@echo "Incus documentation generator built successfully"

.PHONY: deps
deps:
	@if [ ! -e "$(RAFT_PATH)" ]; then \
		git clone --depth=1 "https://github.com/cowsql/raft" "$(RAFT_PATH)"; \
	elif [ -e "$(RAFT_PATH)/.git" ]; then \
		cd "$(RAFT_PATH)"; git pull; \
	fi

	cd "$(RAFT_PATH)" && \
		autoreconf -i && \
		./configure && \
		make

	# cowsql
	@if [ ! -e "$(COWSQL_PATH)" ]; then \
		git clone --depth=1 "https://github.com/cowsql/cowsql" "$(COWSQL_PATH)"; \
	elif [ -e "$(COWSQL_PATH)/.git" ]; then \
		cd "$(COWSQL_PATH)"; git pull; \
	fi

	cd "$(COWSQL_PATH)" && \
		autoreconf -i && \
		PKG_CONFIG_PATH="$(RAFT_PATH)" ./configure && \
		make CFLAGS="-I$(RAFT_PATH)/include/" LDFLAGS="-L$(RAFT_PATH)/.libs/"

	# environment
	@echo ""
	@echo "Please set the following in your environment (possibly ~/.bashrc)"
	@echo "export CGO_CFLAGS=\"-I$(RAFT_PATH)/include/ -I$(COWSQL_PATH)/include/\""
	@echo "export CGO_LDFLAGS=\"-L$(RAFT_PATH)/.libs -L$(COWSQL_PATH)/.libs/\""
	@echo "export LD_LIBRARY_PATH=\"$(RAFT_PATH)/.libs/:$(COWSQL_PATH)/.libs/\""
	@echo "export CGO_LDFLAGS_ALLOW=\"(-Wl,-wrap,pthread_create)|(-Wl,-z,now)\""

.PHONY: update-gomod
update-gomod:
ifneq "$(INCUS_OFFLINE)" ""
	@echo "The update-gomod target cannot be run in offline mode."
	exit 1
endif
	$(GO) get -t -v -d -u ./...
	$(GO) mod tidy
	cd cmd/lxd-to-incus && $(GO) get -t -v -d -u ./...
	cd cmd/lxd-to-incus && $(GO) mod tidy
	@echo "Dependencies updated"

.PHONY: update-protobuf
update-protobuf:
	protoc --go_out=. ./incusd/migration/migrate.proto

.PHONY: update-schema
update-schema:
	cd incusd/db/generate && $(GO) build -o $(GOPATH)/bin/incus-generate -tags "$(TAG_SQLITE3)" $(DEBUG) && cd -
	$(GO) generate ./...
	gofmt -s -w ./incusd/db/
	goimports -w ./incusd/db/
	@echo "Code generation completed"

.PHONY: update-api
update-api:
ifeq "$(INCUS_OFFLINE)" ""
	(cd / ; $(GO) install -v -x github.com/go-swagger/go-swagger/cmd/swagger@latest)
endif
	swagger generate spec -o doc/rest-api.yaml -w ./incusd -m

.PHONY: doc-setup
doc-setup:
	@echo "Setting up documentation build environment"
	python3 -m venv doc/.sphinx/venv
	. $(SPHINXENV) ; pip install --upgrade -r doc/.sphinx/requirements.txt
	rm -Rf doc/html

.PHONY: doc
doc: incus-doc doc-setup doc-incremental

.PHONY: doc-incremental
doc-incremental:
	@echo "Build the documentation"
	$(GOPATH)/bin/incus-doc ./incusd -y ./doc/config_options.yaml -t ./doc/config_options.txt
	. $(SPHINXENV) ; sphinx-build -c doc/ -b dirhtml doc/ doc/html/ -w doc/.sphinx/warnings.txt

.PHONY: doc-serve
doc-serve:
	cd doc/html; python3 -m http.server 8001

.PHONY: doc-spellcheck
doc-spellcheck: doc
	. $(SPHINXENV) ; python3 -m pyspelling -c doc/.sphinx/.spellcheck.yaml

.PHONY: doc-linkcheck
doc-linkcheck: doc-setup
	. $(SPHINXENV) ; sphinx-build -c doc/ -b linkcheck doc/ doc/html/

.PHONY: doc-lint
doc-lint:
	doc/.sphinx/.markdownlint/doc-lint.sh

.PHONY: debug
debug:
ifeq "$(TAG_SQLITE3)" ""
	@echo "Missing custom libsqlite3, run \"make deps\" to setup."
	exit 1
endif

	CC="$(CC)" CGO_LDFLAGS_ALLOW="$(CGO_LDFLAGS_ALLOW)" $(GO) install -v -tags "$(TAG_SQLITE3) logdebug" $(DEBUG) ./...
	CGO_ENABLED=0 $(GO) install -v -tags "netgo,logdebug" ./cmd/incus-migrate
	CGO_ENABLED=0 $(GO) install -v -tags "agent,netgo,logdebug" ./cmd/incus-agent
	@echo "Incus built successfully"

.PHONY: nocache
nocache:
ifeq "$(TAG_SQLITE3)" ""
	@echo "Missing custom libsqlite3, run \"make deps\" to setup."
	exit 1
endif

	CC="$(CC)" CGO_LDFLAGS_ALLOW="$(CGO_LDFLAGS_ALLOW)" $(GO) install -a -v -tags "$(TAG_SQLITE3)" $(DEBUG) ./...
	CGO_ENABLED=0 $(GO) install -a -v -tags netgo ./cmd/incus-migrate
	CGO_ENABLED=0 $(GO) install -a -v -tags agent,netgo ./cmd/incus-agent
	@echo "Incus built successfully"

race:
ifeq "$(TAG_SQLITE3)" ""
	@echo "Missing custom libsqlite3, run \"make deps\" to setup."
	exit 1
endif

	CC="$(CC)" CGO_LDFLAGS_ALLOW="$(CGO_LDFLAGS_ALLOW)" $(GO) install -race -v -tags "$(TAG_SQLITE3)" $(DEBUG) ./...
	CGO_ENABLED=0 $(GO) install -v -tags netgo ./cmd/incus-migrate
	CGO_ENABLED=0 $(GO) install -v -tags agent,netgo ./cmd/incus-agent
	@echo "Incus built successfully"

.PHONY: check
check: default
ifeq "$(INCUS_OFFLINE)" ""
	(cd / ; $(GO) install -v -x github.com/rogpeppe/godeps@latest)
	(cd / ; $(GO) install -v -x github.com/tsenart/deadcode@latest)
	(cd / ; $(GO) install -v -x golang.org/x/lint/golint@latest)
endif
	CGO_LDFLAGS_ALLOW="$(CGO_LDFLAGS_ALLOW)" $(GO) test -v -tags "$(TAG_SQLITE3)" $(DEBUG) ./...
	cd test && ./main.sh

.PHONY: dist
dist: doc
	# Cleanup
	rm -Rf $(ARCHIVE).gz

	# Create build dir
	$(eval TMP := $(shell mktemp -d))
	git archive --prefix=incus-$(VERSION)/ HEAD | tar -x -C $(TMP)
	git show-ref HEAD | cut -d' ' -f1 > $(TMP)/incus-$(VERSION)/.gitref

	# Download dependencies
	(cd $(TMP)/incus-$(VERSION) ; $(GO) mod vendor)

	# Download the cowsql libraries
	git clone --depth=1 https://github.com/cowsql/cowsql $(TMP)/incus-$(VERSION)/vendor/cowsql
	(cd $(TMP)/incus-$(VERSION)/vendor/cowsql ; git show-ref HEAD | cut -d' ' -f1 > .gitref)

	git clone --depth=1 https://github.com/cowsql/raft $(TMP)/incus-$(VERSION)/vendor/raft
	(cd $(TMP)/incus-$(VERSION)/vendor/raft ; git show-ref HEAD | cut -d' ' -f1 > .gitref)

	# Copy doc output
	cp -r doc/html $(TMP)/incus-$(VERSION)/doc/html/

	# Assemble tarball
	tar --exclude-vcs -C $(TMP) -zcf $(ARCHIVE).gz incus-$(VERSION)/

	# Cleanup
	rm -Rf $(TMP)

.PHONY: i18n
i18n: update-pot update-po

po/%.mo: po/%.po
	msgfmt --statistics -o $@ $<

po/%.po: po/$(DOMAIN).pot
	msgmerge -U po/$*.po po/$(DOMAIN).pot

.PHONY: update-po
update-po:
	set -eu; \
	for lang in $(LINGUAS); do\
	    msgmerge --backup=none -U $$lang.po po/$(DOMAIN).pot; \
	done

.PHONY: update-pot
update-pot:
ifeq "$(INCUS_OFFLINE)" ""
	(cd / ; $(GO) install -v -x github.com/snapcore/snapd/i18n/xgettext-go@2.57.1)
endif
	xgettext-go -o po/$(DOMAIN).pot --add-comments-tag=TRANSLATORS: --sort-output --package-name=$(DOMAIN) --msgid-bugs-address=lxc-devel@lists.linuxcontainers.org --keyword=i18n.G --keyword-plural=i18n.NG cmd/incus/*.go internal/cliconfig/*.go

.PHONY: build-mo
build-mo: $(MOFILES)

.PHONY: static-analysis
static-analysis:
ifeq ($(shell command -v golangci-lint 2> /dev/null),)
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $$($(GO) env GOPATH)/bin
endif
ifeq ($(shell command -v shellcheck 2> /dev/null),)
	echo "Please install shellcheck"
	exit 1
endif
ifneq "$(shell shellcheck --version | grep version: | cut -d ' ' -f2)" "0.8.0"
	@echo "WARN: shellcheck version is not 0.8.0"
endif
ifeq ($(shell command -v flake8 2> /dev/null),)
	echo "Please install flake8"
	exit 1
endif
	golangci-lint run --timeout 5m
	flake8 test/deps/import-busybox
	shellcheck --shell sh test/*.sh test/includes/*.sh test/suites/*.sh test/backends/*.sh test/lint/*.sh
	shellcheck test/extras/*.sh
	run-parts --exit-on-error --regex '.sh' test/lint

.PHONY: tags
tags: *.go incusd/*.go shared/*.go cmd/incus/*.go
	find . -type f -name '*.go' | xargs gotags > tags
