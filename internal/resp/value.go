package resp

// Kind identifies a RESP2 value type.
type Kind byte

const (
	KindSimpleString Kind = '+'
	KindError        Kind = '-'
	KindInteger      Kind = ':'
	KindBulkString   Kind = '$'
	KindArray        Kind = '*'
)

// Value is a RESP2 value. Values are immutable after construction.
type Value struct {
	kind    Kind
	data    []byte
	integer int64
	values  []Value
	null    bool
}

func SimpleString(value string) Value {
	return Value{kind: KindSimpleString, data: []byte(value)}
}

func Error(value string) Value {
	return Value{kind: KindError, data: []byte(value)}
}

func Integer(value int64) Value {
	return Value{kind: KindInteger, integer: value}
}

func BulkString(value []byte) Value {
	return Value{kind: KindBulkString, data: value}
}

func BulkStringString(value string) Value {
	return BulkString([]byte(value))
}

func NullBulkString() Value {
	return Value{kind: KindBulkString, null: true}
}

func Array(values ...Value) Value {
	return Value{kind: KindArray, values: values}
}

func NullArray() Value {
	return Value{kind: KindArray, null: true}
}

func (v Value) Kind() Kind {
	return v.kind
}

func (v Value) Bytes() []byte {
	return v.data
}

func (v Value) Int64() int64 {
	return v.integer
}

func (v Value) Values() []Value {
	return v.values
}

func (v Value) IsNull() bool {
	return v.null
}
