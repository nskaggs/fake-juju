include ../common/makefile.mk

JUJU_PATCH = juju-core.patch

build: $(JUJU_TARBALL) $(JUJU_PATCH)
	rm -rf $(JUJU_SRC)  # Go doesn't play nice with existing files.
	tar --strip=1 -z -xf $(JUJU_TARBALL)
	patch -p0 < $(JUJU_PATCH)
	GOPATH=$(GO_PATH) $(GO) build -v

install: $(JUJU_VERSION)
	install -D $(JUJU_VERSION) $(JUJU_INSTALLED)
