package gstin

import "testing"

func TestValidate(t *testing.T) {
	good := WithChecksum("27AAPFU0939F1Z")
	if !Validate(good).Valid {
		t.Fatalf("expected valid: %s", good)
	}
	if Validate("27AAPFU0939F1ZX").Valid && good != "27AAPFU0939F1ZX" {
		t.Fatal("bad checksum accepted")
	}
	if Validate("27AAPFU0939F1").Valid {
		t.Fatal("short accepted")
	}
	// known real-format example
	if !Validate("29AAGCB7383J1Z4").Valid {
		t.Fatal("29AAGCB7383J1Z4 should be valid")
	}
}

func TestRepair(t *testing.T) {
	good := WithChecksum("27AAPFU0939F1Z")
	noisy := "2?" // placeholder
	_ = noisy
	bad := []byte(good)
	bad[7] = 'O' // digit position misread as letter
	r, changed := Repair(string(bad))
	if !changed || r != good {
		t.Fatalf("repair failed: %s -> %s", bad, r)
	}
}

func TestChecksumModes(t *testing.T) {
	defer SetChecksumMode("strict")
	bad := "10IJGTD6056N9ZJ" // generator GSTIN: valid format, wrong checksum
	if r, _ := Check(bad); r.Valid {
		t.Fatal("strict must reject a checksum mismatch")
	}
	SetChecksumMode("off")
	if r, ign := Check(bad); !r.Valid || !ign {
		t.Fatalf("off must accept with checksumIgnored: %+v %v", r, ign)
	}
	if r, _ := Check("99XXXXX0000X1ZZ"); r.Valid && r.StateCode == "" {
		t.Fatal("bad state code must stay invalid")
	}
	if r, _ := Check("10IJGTD6056N9Z"); r.Valid {
		t.Fatal("short GSTIN must stay invalid in any mode")
	}
	SetChecksumMode("warn")
	if r, ign := Check(WithChecksum("27AAPFU0939F1Z")); !r.Valid || ign {
		t.Fatal("a correct GSTIN is valid without the tolerance")
	}
}
