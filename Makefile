INST_PREFIX ?= /usr
INST_LIBDIR ?= $(INST_PREFIX)/lib/lua/5.1
INST_LUADIR ?= $(INST_PREFIX)/share/lua/5.1
INSTALL ?= install

.PHONY: build
build:
	goimports -w main.go
	go build -buildmode=c-shared -o libresty_ffi_etcd.so main.go

.PHONY: install
install:
	$(INSTALL) -d $(INST_LUADIR)/resty/ffi_etcd
	$(INSTALL) lib/resty/ffi_etcd/*.lua $(INST_LUADIR)/resty/ffi_etcd
	$(INSTALL) -d $(INST_LIBDIR)/
	$(INSTALL) libffi_go_etcd.so $(INST_LIBDIR)/

.PHONY: run
run:
	@bash -c '[[ -d demo/logs ]] || mkdir demo/logs'
	cd demo; PATH=/opt/resty_ffi/nginx/sbin:$(PATH) LD_LIBRARY_PATH=$(PWD):/usr/local/lib/lua/5.1 nginx -p $(PWD)/demo -c nginx.conf
