// Copyright Authova contributors. SPDX-License-Identifier: Apache-2.0
package main

import (
	"entgo.io/ent/entc"
	"entgo.io/ent/entc/gen"
	"flag"
	"log"
)

func main() {
	target := flag.String("target", "internal/ent", "generated output directory")
	flag.Parse()
	if err := entc.Generate("./internal/ent/schema", &gen.Config{Target: *target, Package: "github.com/carvebloom/authova/internal/ent", Features: []gen.Feature{gen.FeatureLock, gen.FeatureUpsert}}, entc.TemplateFiles("cmd/entgen/schema.tmpl")); err != nil {
		log.Fatal(err)
	}
}
