// SPDX-License-Identifier: MIT

// Command gsdmltyped shows the generated GSDML model (gsdml/typed and
// gsdml/versions): it writes the GSDML file of the example device,
// validates it against the schema of the version it names, reads it into
// the typed structs of that version, changes a value, and shows that the
// validator catches a change the schema forbids. No schema is read: the
// validator and the types are generated from PI's reference schemas.
//
// With a file argument it validates and summarises that file instead.
package main

import (
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/otfabric/go-profinet/examples/internal/vnetenv"
	"github.com/otfabric/go-profinet/gsdml"
	"github.com/otfabric/go-profinet/gsdml/typed"
	"github.com/otfabric/go-profinet/gsdml/versions/v235"
)

func main() {
	if len(os.Args) > 1 {
		summarise(os.Args[1])
		return
	}
	// The example device's GSDML file, as gsdml writes it (V2.35).
	doc, err := gsdml.FromModel(vnetenv.Model(), gsdml.GenerateOptions{SupervisorAR: true})
	if err != nil {
		log.Fatal(err)
	}
	data, err := gsdml.Marshal(doc)
	if err != nil {
		log.Fatal(err)
	}

	// The schema version the file names, and its validation against it.
	version, err := typed.DetectVersion(data)
	if err != nil {
		log.Fatal(err)
	}
	if err := typed.Validate(data); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("the file is valid against GSDML V%s (one of %d generated schema versions)\n", version, len(typed.Versions()))

	// The typed model of that version: v235's names are aliases of the
	// deduplicated structs of gsdml/typed.
	d, err := v235.Parse(data)
	if err != nil {
		log.Fatal(err)
	}
	body := d.Root.ProfileBody
	id := body.DeviceIdentity
	fmt.Printf("vendor %s, device %s\n", id.VendorID, id.DeviceID)
	for _, dap := range body.ApplicationProcess.DeviceAccessPointList.DeviceAccessPointItem {
		fmt.Printf("DAP %s: module ident %s, slots %s, shared device %v\n",
			dap.ID, dap.ModuleIdentNumber, dap.PhysicalSlots, dap.SharedDeviceSupportedOrDefault())
	}

	// A change the schema allows validates; one it forbids does not.
	id.DeviceID = "0x0042"
	if err := d.Validate(); err != nil {
		log.Fatal(err)
	}
	id.DeviceID = "66" // the schema's pattern is 0x and hexadecimal digits
	err = d.Validate()
	var se *typed.SchemaError
	if !errors.As(err, &se) {
		log.Fatalf("expected a schema error, got %v", err)
	}
	fmt.Println("after an invalid change:", se.Issues[0])

	// gsdml.CheckBytes: the schema's verdict and gsdml's semantic rules.
	rep, err := gsdml.CheckBytes(data)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("CheckBytes: %d errors, %d warnings\n", rep.Count(gsdml.SeverityError), rep.Count(gsdml.SeverityWarning))
	if rep.Err() != nil {
		log.Fatal(rep.Err())
	}
}

func summarise(name string) {
	data, err := os.ReadFile(name)
	if err != nil {
		log.Fatal(err)
	}
	rep, err := gsdml.CheckBytes(data)
	if err != nil {
		log.Fatal(err)
	}
	for _, i := range rep.Issues {
		fmt.Printf("%s %s line %d: %s: %s\n", i.Severity, i.Rule, i.Line, i.Path, i.Message)
	}
	if d, err := typed.Decode(data); err != nil {
		fmt.Println("not decoded:", err)
	} else {
		fmt.Printf("decoded as GSDML V%s (%T)\n", d.SchemaVersion(), d.RootElement())
	}
	if rep.Err() != nil {
		os.Exit(1)
	}
}
