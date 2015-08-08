package main

// #include <acl/libacl.h>
import "C"

import (
	"os"
)

func aclCToGo(cacl C.acl_t) (ACL, error) {
	acl := make(ACL, 0)
	for {
		var centry C.acl_entry_t
		code, err := C.acl_get_entry(cacl, C.ACL_NEXT_ENTRY, &centry)
		// C.acl_get_entry returns 1 on success,
		// 0 when the list is exhausted, and < 0
		// on error (see libacl/acl_get_entry.c)
		if code == 0 {
			break
		} else if code < 0 {
			return nil, err
		}
		entry, err := entryCToGo(centry)
		if err != nil {
			return nil, err
		}
		acl = append(acl, entry)
	}
	return acl, nil
}

func permCToGo(cperm C.acl_permset_t) (os.FileMode, error) {
	var perm os.FileMode
	code, err := C.acl_get_perm(cperm, C.ACL_READ)
	if code < 0 {
		return perm, err
	}
	if code > 0 {
		perm |= 4
	}
	code, err = C.acl_get_perm(cperm, C.ACL_WRITE)
	if code < 0 {
		return perm, err
	}
	if code > 0 {
		perm |= 2
	}
	code, err = C.acl_get_perm(cperm, C.ACL_EXECUTE)
	if code < 0 {
		return perm, err
	}
	if code > 0 {
		perm |= 1
	}
	return perm, nil
}
