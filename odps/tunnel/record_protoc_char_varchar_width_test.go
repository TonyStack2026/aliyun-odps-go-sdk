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

package tunnel

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/aliyun/aliyun-odps-go-sdk/odps/data"
	"github.com/aliyun/aliyun-odps-go-sdk/odps/datatype"
	"github.com/aliyun/aliyun-odps-go-sdk/odps/tableschema"
)

// Everything in this file runs in memory: no tunnel endpoint, no credentials,
// no server round trip. It covers CHAR / VARCHAR values whose UTF-8 byte length
// exceeds the declared width while the character count stays within it, the
// shape reported in https://github.com/aliyun/aliyun-odps-go-sdk/issues/98.
//
// A MaxCompute CHAR(n) / VARCHAR(n) column counts *characters*, so a server
// produced payload may legitimately carry more bytes than n. The reader must
// return such a value intact, and must return an error - never an empty value -
// when a payload really is wider than the column declares.

func parseTypeForWidthTest(t *testing.T, typeText string) datatype.DataType {
	t.Helper()

	dt, err := datatype.ParseDataType(typeText)
	if err != nil {
		t.Fatalf("parse data type %q: %v", typeText, err)
	}

	return dt
}

// encodeThenReadFirstColumn serializes one record against writeType and decodes
// it back against readType.
//
// The two widths differ on purpose. The write side declares a width large
// enough for the payload to be a legal value there, which is the only way to
// get the bytes on the wire without going through the constructors under test.
// The read side then declares the width the column really has, reproducing what
// a server sends for a multi-byte value stored in a narrow CHAR(n)/VARCHAR(n)
// column: the exact same protobuf payload and the exact same record CRC, read
// against a width that the byte-based check rejects.
func encodeThenReadFirstColumn(t *testing.T, writeType, readType datatype.DataType, value data.Data) (data.Data, error) {
	t.Helper()

	writeColumns := []tableschema.Column{{Name: "c", Type: writeType}}
	readColumns := []tableschema.Column{{Name: "c", Type: readType}}

	bw := &bufWriter{buf: bytes.NewBuffer(nil)}
	pw := newRecordProtocWriter(bw, writeColumns, false)
	if err := pw.Write(data.Record{value}); err != nil {
		t.Fatalf("write record: %v", err)
	}
	if err := pw.Close(); err != nil {
		t.Fatalf("close protoc writer: %v", err)
	}

	pr := RecordProtocReader{
		protocReader: NewProtocStreamReader(bytes.NewReader(bw.buf.Bytes())),
		columns:      readColumns,
		recordCrc:    NewCrc32CheckSum(),
		crcOfCrc:     NewCrc32CheckSum(),
	}

	record, err := pr.Read()
	if err != nil {
		return nil, err
	}

	if record.Len() != 1 {
		t.Fatalf("expected 1 column, got %d", record.Len())
	}

	return record.Get(0), nil
}

// newScalarForTest builds a VARCHAR / CHAR value against a deliberately wide
// declaration, so that encoding a payload is possible on every code path
// regardless of how the width check itself is implemented.
func newScalarForTest(t *testing.T, typeText string, width int, payload string) data.Data {
	t.Helper()

	var (
		value data.Data
		err   error
	)

	switch {
	case typeText == "varchar":
		value, err = data.NewVarChar(width, payload)
	case typeText == "char":
		value, err = data.NewChar(width, payload)
	default:
		t.Fatalf("unsupported scalar kind %q", typeText)
	}

	if err != nil {
		t.Fatalf("build %s(%d) value: %v", typeText, width, err)
	}

	return value
}

func scalarShape(v data.Data) (text string, typeName string) {
	switch typed := v.(type) {
	case data.VarChar:
		return typed.String(), typed.Type().Name()
	case *data.VarChar:
		return typed.String(), typed.Type().Name()
	case data.Char:
		return typed.String(), typed.Type().Name()
	case *data.Char:
		return typed.String(), typed.Type().Name()
	case nil:
		return "", "<nil>"
	default:
		return fmt.Sprintf("%v", typed), fmt.Sprintf("%T", typed)
	}
}

// TestProtocReaderPreservesMultiByteValuesWithinCharacterWidth pins the read
// path for values that fit the declared width in characters but not in bytes:
// Arabic, CJK and a supplementary (astral) character, for both VARCHAR and CHAR.
func TestProtocReaderPreservesMultiByteValuesWithinCharacterWidth(t *testing.T) {
	cases := []struct {
		kind     string // "varchar" | "char"
		name     string
		typeText string // declared column type as read by the client
		payload  string
		width    int // width used while encoding the payload
	}{
		{kind: "varchar", name: "cjk", typeText: "varchar(5)", payload: "中文中文中", width: 15},
		{kind: "varchar", name: "arabic", typeText: "varchar(5)", payload: "مرحبا", width: 10},
		{kind: "varchar", name: "astral_and_cjk", typeText: "varchar(3)", payload: "😀中文", width: 10},
		{kind: "char", name: "cjk", typeText: "char(10)", payload: "中文中文中文中文中文", width: 30},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.kind+"/"+tc.name, func(t *testing.T) {
			value := newScalarForTest(t, tc.kind, tc.width, tc.payload)

			got, err := encodeThenReadFirstColumn(t,
				newScalarForTestType(t, tc.kind, tc.width),
				parseTypeForWidthTest(t, tc.typeText),
				value,
			)
			if err != nil {
				t.Fatalf("read a %d character value declared %s: %v",
					len([]rune(tc.payload)), tc.typeText, err)
			}

			text, typeName := scalarShape(got)
			if text != tc.payload {
				t.Errorf("read back %q (type %s), want %q: a value that fits the "+
					"declared width in characters must survive the read", text, typeName, tc.payload)
			}
			if want := parseTypeForWidthTest(t, tc.typeText).Name(); typeName != want {
				t.Errorf("value type is %s, want %s: a decoded value must carry the "+
					"width declared by the schema, not the width 0 of a zero value", typeName, want)
			}
		})
	}
}

// newScalarForTestType returns the datatype used to encode a payload of a known
// byte length without tripping the width check.
func newScalarForTestType(t *testing.T, kind string, width int) datatype.DataType {
	t.Helper()

	switch kind {
	case "varchar":
		return datatype.NewVarcharType(width)
	case "char":
		return datatype.NewCharType(width)
	default:
		t.Fatalf("unsupported scalar kind %q", kind)
		return nil
	}
}

// TestProtocReaderReportsOverflowInsteadOfReturningEmptyValue pins the other
// side of the contract: when the payload really is wider than the column, the
// read must fail loudly. Returning an empty value with a nil error is what
// turns a corrupt or mismatched payload into silent data loss.
func TestProtocReaderReportsOverflowInsteadOfReturningEmptyValue(t *testing.T) {
	cases := []struct {
		kind     string
		name     string
		typeText string
		payload  string
		width    int
	}{
		{kind: "varchar", name: "ascii_overflow", typeText: "varchar(5)", payload: "abcdefghij", width: 10},
		{kind: "varchar", name: "one_char_over", typeText: "varchar(5)", payload: "中文中文中文", width: 18},
		{kind: "char", name: "ascii_overflow", typeText: "char(5)", payload: "abcdefghij", width: 10},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.kind+"/"+tc.name, func(t *testing.T) {
			value := newScalarForTest(t, tc.kind, tc.width, tc.payload)

			got, err := encodeThenReadFirstColumn(t,
				newScalarForTestType(t, tc.kind, tc.width),
				parseTypeForWidthTest(t, tc.typeText),
				value,
			)
			if err == nil {
				text, typeName := scalarShape(got)
				t.Fatalf("overflowing value was accepted: read back %q (type %s), want an error",
					text, typeName)
			}
			if got != nil {
				t.Errorf("an erroring read must not also hand back a value, got %#v", got)
			}
		})
	}
}

// TestProtocReaderAsciiBoundaryIsInclusive is the control case: single byte
// text at exactly the declared width has always decoded correctly and must keep
// doing so. It also pins that the width check is inclusive, not off by one.
func TestProtocReaderAsciiBoundaryIsInclusive(t *testing.T) {
	value, err := data.NewVarChar(5, "abcde")
	if err != nil {
		t.Fatalf("build varchar(5) value: %v", err)
	}

	got, err := encodeThenReadFirstColumn(t,
		datatype.NewVarcharType(5),
		datatype.NewVarcharType(5),
		value,
	)
	if err != nil {
		t.Fatalf("read ascii value at the declared width: %v", err)
	}

	text, typeName := scalarShape(got)
	if text != "abcde" || typeName != datatype.NewVarcharType(5).Name() {
		t.Errorf("read back %q (type %s), want \"abcde\" (type %s)",
			text, typeName, datatype.NewVarcharType(5).Name())
	}
}

// TestProtocReaderKeepsDistinctMultiByteMapKeys covers the map shape: two
// distinct multi-byte keys must not collapse into one entry. Before the fix both
// keys decoded to the same empty width 0 value, so the second Set overwrote the
// first and the read reported success with one entry, losing a row.
func TestProtocReaderKeepsDistinctMultiByteMapKeys(t *testing.T) {
	cases := []struct {
		kind      string
		writeType string
		readType  string
		width     int
		keys      map[string]string // map key -> map value
	}{
		{
			kind: "varchar", writeType: "map<varchar(15),string>", readType: "map<varchar(5),string>", width: 15,
			keys: map[string]string{"中文中文中": "cjk", "مرحبا": "arabic"},
		},
		{
			kind: "char", writeType: "map<char(30),string>", readType: "map<char(10),string>", width: 30,
			keys: map[string]string{"中文中文中文中文中文": "cjk", "aaaaaaaaaa": "ascii"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.kind, func(t *testing.T) {
			writeMapType := parseTypeForWidthTest(t, tc.writeType)
			readMapType := parseTypeForWidthTest(t, tc.readType)

			wm := data.NewMapWithType(writeMapType.(datatype.MapType))
			for key, value := range tc.keys {
				var (
					k data.Data
					e error
				)
				if tc.kind == "varchar" {
					k, e = data.NewVarChar(tc.width, key)
				} else {
					k, e = data.NewChar(tc.width, key)
				}
				if e != nil {
					t.Fatalf("build key %q: %v", key, e)
				}
				if err := wm.Set(k, data.String(value)); err != nil {
					t.Fatalf("set key %q: %v", key, err)
				}
			}

			got, err := encodeThenReadFirstColumn(t, writeMapType, readMapType, wm)
			if err != nil {
				t.Fatalf("read map with distinct multi-byte keys: %v", err)
			}

			gotMap, ok := got.(*data.Map)
			if !ok {
				t.Fatalf("read back %#v, want *data.Map", got)
			}

			wantKeyType := readMapType.(datatype.MapType).KeyType.Name()

			rendered := make(map[string]string, len(tc.keys))
			for k, v := range gotMap.ToGoMap() {
				keyText, keyType := scalarShape(k.(data.Data))
				valueText, _ := scalarShape(v.(data.Data))
				rendered[keyText] = valueText
				if keyType != wantKeyType {
					t.Errorf("decoded key %q carries type %s, want %s", keyText, keyType, wantKeyType)
				}
			}

			if len(rendered) != len(tc.keys) {
				t.Fatalf("read %d entries (%v), want %d: a successful read with a "+
					"collapsed key is never correct", len(rendered), rendered, len(tc.keys))
			}
			for key, value := range tc.keys {
				if rendered[key] != value {
					t.Errorf("entry %q=%q missing or wrong, got %v", key, value, rendered)
				}
			}
		})
	}
}
