
TAG?=1.0

all: build

build:
	go build -o out/webhook

image:
	docker build --tag quay.io/hgao/cdi-images-caching:${TAG} .

push: image
	docker push quay.io/hgao/cdi-images-caching:${TAG}



