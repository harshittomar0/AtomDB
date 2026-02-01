package main

import (
	"fmt"
	"os"
)

func main() {
	path := "test.db"
	
	db := &KV{Path: path}
	if err := db.Open(); err != nil {
		panic(err)
	}
	
	fmt.Println("Setting key=hello, val=world...")
	if err := db.Set([]byte("hello"), []byte("world")); err != nil {
		panic(err)
	}
	
	val, ok := db.Get([]byte("hello"))
	fmt.Printf("Get(hello) = %s, found=%v\n", val, ok)
	
	db.Close()

	fmt.Println("\nReopening database...")
	db2 := &KV{Path: path}
	if err := db2.Open(); err != nil {
		panic(err)
	}
	
	val2, ok2 := db2.Get([]byte("hello"))
	fmt.Printf("Get(hello) after reopen = %s, found=%v\n", val2, ok2)
	db2.Close()
	
	os.Remove(path)
}