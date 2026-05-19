package inventory

// export_test.go re-exports a handful of package-private symbols
// solely for the inventory_test external test package. None of these
// names exist outside `go test`. The pattern keeps the production
// surface narrow while still letting black-box tests drive the
// streaming helpers extracted for OPS-009.

// TextWriterForTest is the public alias of the textWriter interface.
type TextWriterForTest = textWriter

// StreamInventoryToClientForTest exposes streamInventoryToClient.
func StreamInventoryToClientForTest(svc InventoryService, w textWriter) {
	streamInventoryToClient(svc, w)
}

// StreamReservationsToClientForTest exposes streamReservationsToClient.
func StreamReservationsToClientForTest(svc ReservationService, w textWriter) {
	streamReservationsToClient(svc, w)
}
