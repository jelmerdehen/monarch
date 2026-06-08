ROOT_DIR := $(shell dirname $(realpath $(firstword $(MAKEFILE_LIST))))
BIN_SRC := $(ROOT_DIR)/bin/monarch
BIN_DST := $(HOME)/.local/bin/monarch
UNIT_DIR := $(HOME)/.config/systemd/user
DATA_DIR := /data/mon

# Pipelines that have a periodic timer. screencast is user-triggered
# (G4/G5 in i3) and has no timer; it shows up under DATA_PIPELINES
# only so its output dirs are created during install.
TIMER_UNITS := arecord compress v4l2 x11grab
DATA_PIPELINES := arecord arecord_compress v4l2 v4l2_compress x11grab x11grab_compress screencast screencast_compress

.PHONY: all build deploy install install-bin install-units install-dirs \
        start stop restart status uninstall clean

all: build

##
## build
##

build: $(BIN_SRC)

# GOWORK=off because the surrounding /data/p/go.work does not list
# this module; with the workspace active, go build refuses to compile.
$(BIN_SRC):
	mkdir -p $(dir $@)
	GOWORK=off go build -o $@ ./core/

##
## deploy: single command — build, install everything, restart timers.
## Idempotent. Safe to re-run after every code change.
##

deploy: clean build install restart
	@echo "monarch deployed: bin=$(BIN_DST)"
	@systemctl --user --no-pager --lines=0 list-timers monarch_* || true

##
## install: binary + sd units + output dirs + enable timers.
##

install: install-bin install-units install-dirs
	@for u in $(TIMER_UNITS); do \
		systemctl --user enable monarch_$$u.timer; \
	done
	systemctl --user daemon-reload

install-bin: build
	mkdir -p $(dir $(BIN_DST))
	install -m 0755 $(BIN_SRC) $(BIN_DST)

install-units:
	mkdir -p $(UNIT_DIR)
	cp -v sd/monarch_* $(UNIT_DIR)/
	# `BIN` is a literal placeholder in the committed unit files;
	# rewrite it to the installed binary path. `|` separator avoids
	# clashes with the `/` in $(BIN_DST).
	sed -i 's|BIN|$(BIN_DST)|g' $(UNIT_DIR)/monarch_*.service

install-dirs:
	@for d in $(DATA_PIPELINES); do \
		mkdir -p $(DATA_DIR)/$$d; \
	done

##
## lifecycle
##

start:
	@for u in $(TIMER_UNITS); do \
		systemctl --user start monarch_$$u.timer; \
	done

stop:
	-systemctl --user stop 'monarch_*'

restart: stop start

status:
	-systemctl --user --no-pager status 'monarch_*'

##
## teardown
##

uninstall:
	-systemctl --user stop 'monarch_*'
	@for u in $(TIMER_UNITS); do \
		systemctl --user disable monarch_$$u.timer || true; \
	done
	-rm -v $(UNIT_DIR)/monarch_*
	-rm -v $(BIN_DST)
	systemctl --user daemon-reload

clean:
	rm -rf bin
