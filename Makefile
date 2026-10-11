GOLIB ?= golib

.PHONY: check check-full ci cohesion inventory repository-check specification-check specification-check-online

check:
	$(GOLIB) check --local --all

check-full:
	$(GOLIB) check --all

ci:
	$(GOLIB) repository check
	$(GOLIB) cohesion check
	$(GOLIB) check --local --all

specification-check:
	$(GOLIB) specification check

specification-check-online:
	$(GOLIB) specification check --online

cohesion:
	$(GOLIB) cohesion check

inventory repository-check:
	$(GOLIB) repository check
