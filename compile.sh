#!/bin/sh
rm -rf *.exe
go build -o racoon.exe main.go
./racoon.exe chall.c
dot -Tsvg uml.dot -o uml.svg
open uml.svg