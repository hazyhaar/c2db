package c2ql

#Cap: close({
	prefix:      string
	quota_bytes: uint & >0
	quota_ops:   uint & >0
})

#Filter: close({
	eq?: [string, string | int]
	ge?: [string, string | int]
	and?: [...#Filter]
})

#Scan: close({
	coll:  string
	limit: uint & >0
	proj: [...string]
	filter?:  #Filter
	kind?:    uint8
	coll_id?: uint8
	from_id?: string
	to_id?:   string
})

#Get: close({
	coll: string
	id:   string
})

#Count: close({
	coll:     string
	limit:    uint & >0
	from_id?: string
	to_id?:   string
})

#Cas: close({
	coll:   string
	id:     string
	expect: string
	ops: [...close({
		op: uint
		f:  string
		v?: _
	})]
})

#Del: close({
	coll:   string
	id:     string
	expect: string
})

#Ins: close({
	coll: string
	doc:  _
})

#Gets: close({
	coll: string
	ids: [...string]
	limit: uint & >0
	proj: [...string]
})

#Join: close({
	on:        string
	left_max:  uint & >0
	right_max: uint & >0
	left:      #Scan
	right: close({
		coll:      string
		from_left: string
		limit:     uint & >0
		proj: [...string]
	})
})

#Group: close({
	by: [...string]
	limit: uint & >0
	sum?:  string
	src?:  #Scan
})

#Verb: close({
	get?:   #Get
	gets?:  #Gets
	scan?:  #Scan
	count?: #Count
	cas?:   #Cas
	del?:   #Del
	ins?:   #Ins
	join?:  #Join
	group?: #Group
	nest?:  #Nest
})

#Nest: close({
	depth: uint & >=1 & <=4
	src: close({
		get?:   #Get
		gets?:  #Gets
		scan?:  #Scan
		count?: #Count
		join?:  #Join
	})
	then: close({
		cas?:   #Cas
		group?: #Group
		scan?:  #Scan
		count?: #Count
	})
})

#Lot: close({
	cap:    #Cap
	as_of?: string
	op?:    #Verb
	ops?: [...#Verb]
})
