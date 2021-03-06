package acl

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"sync"
	"syscall"
)

/*
	NOTE: This implementation is largely based on Linux's libacl.
*/

type tag int

const (
	// defined in sys/acl.h
	tagUndefined Tag = 0x00
	tagUserObj       = 0x01
	tagUser          = 0x02
	tagGroupObj      = 0x04
	tagGroup         = 0x08
	tagMask          = 0x10
	tagOther         = 0x20

	// defined in include/acl_ea.h (see libacl source)
	aclEAAccess    = "system.posix_acl_access"
	aclEADefault   = "system.posix_acl_default"
	aclEAVersion   = 2
	aclEAEntrySize = 8
	aclUndefinedID = math.MaxUint32 // defined in sys/acl.h
)

func get(path string) (ACL, error) {
	return getType(path, aclEAAccess)
}

func getDefault(path string) (ACL, error) {
	return getType(path, aclEADefault)
}

func set(path string, acl ACL) error {
	return setType(path, aclEAAccess, acl)
}

func setDefault(path string, acl ACL) error {
	return setType(path, aclEADefault, acl)
}

func xattrFromACL(acl ACL) (xattr []byte, err error) {
	// NOTE(joshlf): I honestly don't know why sorting is required -
	// all I know is that when the entries are left unsorted, the
	// setxattrs syscall sometimes returns EINVAL, but when they're
	// sorted, it never does. I can't find either documentation or
	// kernel code to explain this behavior. The only evidence is
	// the source code for libacl's acl_check, which checks the order.
	acl = append(ACL(nil), acl...)
	sort.Sort(sortableACL(acl))

	xattr = make([]byte, 4+8*len(acl))
	binary.LittleEndian.PutUint32(xattr, aclEAVersion)
	xattrtmp := xattr[4:]
	for _, ent := range acl {
		binary.LittleEndian.PutUint16(xattrtmp, uint16(ent.Tag))
		binary.LittleEndian.PutUint16(xattrtmp[2:], uint16(ent.Perms))
		if ent.Tag == TagUser || ent.Tag == TagGroup {
			qid, err := strconv.ParseUint(ent.Qualifier, 10, 32)
			if err != nil {
				return nil, fmt.Errorf("parse qualifier: %v", err)
			}
			binary.LittleEndian.PutUint32(xattrtmp[4:], uint32(qid))
		} else {
			binary.LittleEndian.PutUint32(xattrtmp[4:], aclUndefinedID)
		}
		xattrtmp = xattrtmp[8:]
	}
	return xattr, nil
}

// sort according to the same order as required by libacl's acl_check

func entryPriority(e Entry) int {
	switch e.Tag {
	case TagUserObj:
		return 0
	case TagUser:
		return 1
	case TagGroupObj:
		return 2
	case TagGroup:
		return 3
	case TagMask:
		return 4
	default:
		return 5
	}
}

type sortableACL ACL

func (s sortableACL) Len() int { return len(s) }
func (s sortableACL) Less(i, j int) bool {
	return entryPriority(s[i]) < entryPriority(s[j])
}
func (s sortableACL) Swap(i, j int) { s[i], s[j] = s[j], s[i] }

func aclFromXattr(xattr []byte) (acl ACL, err error) {
	if len(xattr) < 4 {
		return nil, syscall.EINVAL
	}
	version := binary.LittleEndian.Uint32(xattr)
	xattr = xattr[4:]
	if version != aclEAVersion {
		return nil, syscall.EINVAL
	}
	if len(xattr)%aclEAEntrySize != 0 {
		return nil, syscall.EINVAL
	}

	for len(xattr) > 0 {
		etag := binary.LittleEndian.Uint16(xattr)
		sperm := binary.LittleEndian.Uint16(xattr[2:])
		qid := binary.LittleEndian.Uint32(xattr[4:])

		ent := Entry{
			Tag:   Tag(etag),
			Perms: os.FileMode(sperm),
		}
		if ent.Tag == TagUser || ent.Tag == TagGroup {
			ent.Qualifier = fmt.Sprint(qid)
		}

		acl = append(acl, ent)
		xattr = xattr[8:]
	}

	return acl, nil
}

const (
	defaultbuflen = 64
)

// based on libacl's acl_get_file
func getType(path, attr string) (ACL, error) {
	buf := bufpool.Get().([]byte)
	defer func() { bufpool.Put(buf) }()

	sz, err := syscall.Getxattr(path, attr, buf)
	if sz == -1 && err == syscall.ERANGE {
		sz, err = syscall.Getxattr(path, attr, nil)
		if sz <= 0 {
			return nil, err
		}
		buf = make([]byte, sz)
		sz, err = syscall.Getxattr(path, attr, buf)
	}

	switch {
	case sz > 0:
		return aclFromXattr(buf[:sz])
	case err == syscall.ENODATA:
		// TODO(joshlf): acl_get_file also checks for ENOATTR,
		// but it's not defined in syscall?
		fi, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if attr == aclEADefault {
			if fi.IsDir() {
				return nil, nil
			}
			return nil, syscall.EACCES
		} else {
			return FromUnix(fi.Mode()), nil
		}
	default:
		return nil, err
	}
}

// based on libacl's acl_set_file
func setType(path, attr string, acl ACL) error {
	if attr == aclEADefault {
		fi, err := os.Stat(path)
		if err != nil {
			return err
		}

		// non-directories can't have default ACLs
		if !fi.IsDir() {
			return syscall.EACCES
		}
	}

	xattr, err := xattrFromACL(acl)
	if err != nil {
		return err
	}
	return syscall.Setxattr(path, attr, xattr, 0)
}

var bufpool = sync.Pool{
	New: func() interface{} { return make([]byte, defaultbuflen) },
}
