
all: build

build:
	go build -o out/webhook

image:
	docker build --tag quay.io/hgao/cdi-images-validator:1.0 .



