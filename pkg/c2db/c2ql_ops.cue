package c2ql

#Op: close({
	id:   uint
	name: string
})

ops: close({
	"1": {id: 1, name: "set_field"}
	"2": {id: 2, name: "set_if_absent"}
	"3": {id: 3, name: "incr_u64"}
	"8": {id: 8, name: "clear_field"}
})
