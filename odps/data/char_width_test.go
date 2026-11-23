// Licensed to the Apache Software Foundation (ASF) under one
// or more contributor license agreements.  See the NOTICE file
// distributed with this work for additional information
// regarding copyright ownership.  The ASF licenses this file
// to you under the Apache License, Version 2.0 (the
// "License"); you may not use this file except in compliance
// with the License.  You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package data

import (
	"strings"
	"testing"
)

// CHAR(n) and VARCHAR(n) declare a width in characters. All four constructors
// must accept a value whose UTF-8 byte length exceeds n while its character
// count stays within n, and must report the character count when they reject a
// value that really is too long.

func TestCharAndVarCharAcceptMultiByteValuesWithinCharacterWidth(t *testing.T) {
	cases := []struct {
		name    string
		width   int
		payload string
	}{
		{"cjk_at_width", 10, "中文中文中文中文中文"}, // 10 characters, 30 bytes
		{"arabic_at_width", 5, "مرحبا"},    // 5 characters, 10 bytes
		{"astral_counts_once", 3, "😀中文"},   // 3 characters, 10 bytes
		{"ascii_at_width", 5, "abcde"},     // boundary is inclusive
		{"mixed", 4, "a中😀b"},               // 4 characters, 9 bytes
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			charValue, err := NewChar(tc.width, tc.payload)
			if err != nil {
				t.Errorf("NewChar(%d, %q): %v", tc.width, tc.payload, err)
			} else if charValue.Data() != tc.payload {
				t.Errorf("NewChar kept %q, want %q", charValue.Data(), tc.payload)
			}

			madeChar, err := MakeChar(tc.width, tc.payload)
			if err != nil {
				t.Errorf("MakeChar(%d, %q): %v", tc.width, tc.payload, err)
			} else if madeChar.String() != tc.payload {
				t.Errorf("MakeChar kept %q, want %q", madeChar.String(), tc.payload)
			}

			varCharValue, err := NewVarChar(tc.width, tc.payload)
			if err != nil {
				t.Errorf("NewVarChar(%d, %q): %v", tc.width, tc.payload, err)
			} else if varCharValue.Data() != tc.payload {
				t.Errorf("NewVarChar kept %q, want %q", varCharValue.Data(), tc.payload)
			}

			madeVarChar, err := MakeVarChar(tc.width, tc.payload)
			if err != nil {
				t.Errorf("MakeVarChar(%d, %q): %v", tc.width, tc.payload, err)
			} else if madeVarChar.String() != tc.payload {
				t.Errorf("MakeVarChar kept %q, want %q", madeVarChar.String(), tc.payload)
			}
		})
	}
}

func TestCharAndVarCharRejectValuesWiderThanCharacterWidth(t *testing.T) {
	// 11 CJK characters occupy 33 bytes; the width is 10 characters.
	payload := strings.Repeat("中", 11)

	if _, err := NewChar(10, payload); err == nil {
		t.Error("NewChar accepted 11 characters in char(10)")
	} else if !strings.Contains(err.Error(), "string length is 11, bigger than the max length 10") {
		// The numbers in the message must be character counts: a byte count of 33
		// for a char(10) column reads as an unfixable report.
		t.Errorf("NewChar error is %q, want the character counts 11 and 10", err.Error())
	}

	if _, err := MakeVarChar(10, payload); err == nil {
		t.Error("MakeVarChar accepted 11 characters in varchar(10)")
	} else if !strings.Contains(err.Error(), "string length is 11, bigger than the max length 10") {
		t.Errorf("MakeVarChar error is %q, want the character counts 11 and 10", err.Error())
	}
}

func TestCharAndVarCharKeepDeclaredWidthOnAcceptedValues(t *testing.T) {
	charValue, err := NewChar(10, "中文中文中文中文中文")
	if err != nil {
		t.Fatalf("NewChar: %v", err)
	}
	if got := charValue.Type().Name(); !strings.Contains(got, "CHAR(10)") {
		t.Errorf("char type is %s, want it to report the declared width 10", got)
	}

	varCharValue, err := NewVarChar(5, "مرحبا")
	if err != nil {
		t.Fatalf("NewVarChar: %v", err)
	}
	if got := varCharValue.Type().Name(); !strings.Contains(got, "VARCHAR(5)") {
		t.Errorf("varchar type is %s, want it to report the declared width 5", got)
	}
}
