.PHONY: debugBuild build test updateYaml applyTest init update dbuild publish debugTest

IMGNAME = cytoacrtest.azurecr.io/eventhub-receiver

debugBuild:
	go mod tidy
	CGO_ENABLED=0 go build -o bin/eph_cgo
	docker build -t ${IMGNAME}:$(tag) -f test/Dockerfile.eph .

# needs: tag, namespace
debugTest: debugBuild updateYaml applyTest

test: updateYaml applyTest

applyTest:
	kubectl apply -f test/

updateYaml:
	sed -i "s|image: ${IMGNAME}:.*|image: ${IMGNAME}:$(tag)|" test/eph.yaml
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
	go build -o bin/eventhub-receiver
