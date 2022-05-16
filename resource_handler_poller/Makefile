.PHONY: build test updateYaml applyTest init update dbuild publish debugBuild debugTest proto

IMGNAME = cytoacrtest.azurecr.io/resource-handler-poller

proto:
	protoc -I=$(CURDIR)/protocols/ --go_out=$(CURDIR) --go-grpc_out=$(CURDIR) $(CURDIR)/protocols/k8s_protocols/*.proto
	cd $(CURDIR)/k8s_prot; if [ ! -f "go.mod" ];then go mod init k8s_prot;fi; go mod tidy

debugBuild:
	go mod tidy
	CGO_ENABLED=0 go build -o bin/poller_cgo
	docker build -t ${IMGNAME}:$(tag) -f test/Dockerfile.poller .

# needs: tag, namespace
test: updateYaml applyTest

debugTest: debugBuild updateYaml applyTest

applyTest:
	kubectl apply -f test/

updateYaml:
	sed -i "s|image: ${IMGNAME}:.*|image: ${IMGNAME}:$(tag)|" test/poller.yaml
	for i in test/*.yaml;do sed -i "s/namespace: .*/namespace: $(namespace)/" "$$i";done

removeTest:
	kubectl delete -f test/

init:
	git submodule update --init --recursive

update:
	git submodule update --remote --recursive

# needs tag!
dbuild: init update
	docker build . -f Dockerfile.release -t ${IMGNAME}:$(tag)

publish: dbuild
	docker push ${IMGNAME}:$(tag)

build:
	go mod tidy
	go build -o bin/rh-poller
