package builtintools

import "testing"

func TestActualPartialNativeToolkitCannotAdmitTheOrdinaryDefaultPool(t *testing.T) {
	service, _, _, _ := fixture(t)
	if service.HasDefaultCatalogBindings() {
		t.Fatal("actual finite native file toolkit claimed complete default producers")
	}
	var absent *Service
	if absent.HasDefaultCatalogBindings() {
		t.Fatal("missing toolkit admitted defaults")
	}
}
