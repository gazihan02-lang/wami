.PHONY: build run dev clean deps

deps:
	go install github.com/a-h/templ/cmd/templ@latest

build:
	templ generate
	go build -o bot .

run: build
	./bot

dev:
	templ generate
	go run .

clean:
	rm -f bot bot.db wa.db
	find . -name "*_templ.go" -delete
