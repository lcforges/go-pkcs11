// Package pkcs11 implements logic for using PKCS #11 shared libraries.
package pkcs11

/*
#include <dlfcn.h>
#include <stdlib.h>

#define CK_PTR *
#define CK_DECLARE_FUNCTION(returnType, name) \
  returnType name
#define CK_DECLARE_FUNCTION_POINTER(returnType, name) \
  returnType (* name)
#define CK_CALLBACK_FUNCTION(returnType, name) \
  returnType (* name)
#ifndef NULL_PTR
#define NULL_PTR 0
#endif

#include "pkcs11.h"

// Go can't call a C function pointer directly, so these are wrappers that
// perform the dereference in C.

CK_RV get_function_list(CK_C_GetFunctionList fn, CK_FUNCTION_LIST_PTR_PTR p) {
	return (*fn)(p);
}

CK_RV ck_initialize(CK_FUNCTION_LIST_PTR fl, CK_C_INITIALIZE_ARGS_PTR args) {
	return (*fl->C_Initialize)((CK_VOID_PTR)(args));
}

CK_RV ck_finalize(CK_FUNCTION_LIST_PTR fl) {
	return (*fl->C_Finalize)(NULL_PTR);
}

CK_RV ck_init_token(
	CK_FUNCTION_LIST_PTR fl,
	CK_SLOT_ID      slotID,
	CK_UTF8CHAR_PTR pPin,
	CK_ULONG        ulPinLen,
	CK_UTF8CHAR_PTR pLabel
) {
	if (ulPinLen == 0) {
		// TODO(ericchiang): This isn't tested since softhsm requires a PIN.
		pPin = NULL_PTR;
	}
	return (*fl->C_InitToken)(slotID, pPin, ulPinLen, pLabel);
}

CK_RV ck_get_slot_list(
	CK_FUNCTION_LIST_PTR fl,
	CK_SLOT_ID_PTR pSlotList,
	CK_ULONG_PTR pulCount
) {
	return (*fl->C_GetSlotList)(CK_FALSE, pSlotList, pulCount);
}

CK_RV ck_get_info(
	CK_FUNCTION_LIST_PTR fl,
	CK_INFO_PTR pInfo
) {
	return (*fl->C_GetInfo)(pInfo);
}

CK_RV ck_get_slot_info(
	CK_FUNCTION_LIST_PTR fl,
	CK_SLOT_ID slotID,
	CK_SLOT_INFO_PTR pInfo
) {
	return (*fl->C_GetSlotInfo)(slotID, pInfo);
}

CK_RV ck_get_token_info(
	CK_FUNCTION_LIST_PTR fl,
	CK_SLOT_ID slotID,
	CK_TOKEN_INFO_PTR pInfo
) {
	return (*fl->C_GetTokenInfo)(slotID, pInfo);
}

CK_RV ck_open_session(
	CK_FUNCTION_LIST_PTR fl,
	CK_SLOT_ID slotID,
	CK_FLAGS flags,
	CK_SESSION_HANDLE_PTR phSession
) {
	return (*fl->C_OpenSession)(slotID, flags, NULL_PTR, NULL_PTR, phSession);
}

CK_RV ck_close_session(
	CK_FUNCTION_LIST_PTR fl,
	CK_SESSION_HANDLE hSession
) {
	return (*fl->C_CloseSession)(hSession);
}

CK_RV ck_login(
	CK_FUNCTION_LIST_PTR fl,
	CK_SESSION_HANDLE hSession,
	CK_USER_TYPE userType,
	CK_UTF8CHAR_PTR pPin,
	CK_ULONG ulPinLen
) {
	return (*fl->C_Login)(hSession, userType, pPin, ulPinLen);
}

CK_RV ck_logout(
	CK_FUNCTION_LIST_PTR fl,
	CK_SESSION_HANDLE hSession
) {
	return (*fl->C_Logout)(hSession);
}

CK_RV ck_init_pin(
	CK_FUNCTION_LIST_PTR fl,
	CK_SESSION_HANDLE hSession,
	CK_UTF8CHAR_PTR pPin,
	CK_ULONG ulPinLen
) {
	return (*fl->C_InitPIN)(hSession, pPin, ulPinLen);
}

CK_RV ck_generate_key_pair(
	CK_FUNCTION_LIST_PTR fl,
	CK_SESSION_HANDLE hSession,
	CK_MECHANISM_PTR pMechanism,
	CK_ATTRIBUTE_PTR pPublicKeyTemplate,
	CK_ULONG ulPublicKeyAttributeCount,
	CK_ATTRIBUTE_PTR pPrivateKeyTemplate,
	CK_ULONG ulPrivateKeyAttributeCount,
	CK_OBJECT_HANDLE_PTR phPublicKey,
	CK_OBJECT_HANDLE_PTR phPrivateKey
) {
	return (*fl->C_GenerateKeyPair)(
		hSession,
		pMechanism,
		pPublicKeyTemplate,
		ulPublicKeyAttributeCount,
		pPrivateKeyTemplate,
		ulPrivateKeyAttributeCount,
		phPublicKey,
		phPrivateKey
	);
}
*/
// #cgo linux LDFLAGS: -ldl
import "C"
import (
	"crypto"
	"encoding/asn1"
	"fmt"
	"strings"
	"unsafe"
)

// Error is returned for cryptokit specific API codes.
type Error struct {
	fnName string
	code   C.CK_RV
}

func (e *Error) Error() string {
	return fmt.Sprintf("pkcs11: %s() 0x%x", e.fnName, e.code)
}

func isOk(fnName string, rv C.CK_RV) error {
	if rv == C.CKR_OK {
		return nil
	}
	return &Error{fnName, rv}
}

// Module represents an opened shared library. By default, this package
// requests locking support from the module, but concurrent safety may
// depend on the underlying library.
type Module struct {
	// mod is a pointer to the dlopen handle. Kept around to dlfree
	// when the Module is closed.
	mod unsafe.Pointer
	// List of C functions provided by the module.
	fl C.CK_FUNCTION_LIST_PTR
	// Version of the module, used for compatibility.
	version C.CK_VERSION

	info Info
}

// Open dlopens a shared library by path, initializing the module.
func Open(path string) (*Module, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	mod := C.dlopen(cPath, C.RTLD_NOW)
	if mod == nil {
		return nil, fmt.Errorf("pkcs11: dlopen error: %s", C.GoString(C.dlerror()))
	}

	cSym := C.CString("C_GetFunctionList")
	defer C.free(unsafe.Pointer(cSym))

	getFuncListFn := (C.CK_C_GetFunctionList)(C.dlsym(mod, cSym))
	if getFuncListFn == nil {
		err := fmt.Errorf("pkcs11: lookup function list symbol: %s", C.GoString(C.dlerror()))
		C.dlclose(mod)
		return nil, err
	}

	var p C.CK_FUNCTION_LIST_PTR
	rv := C.get_function_list(getFuncListFn, &p)
	if err := isOk("C_GetFunctionList", rv); err != nil {
		C.dlclose(mod)
		return nil, err
	}

	args := C.CK_C_INITIALIZE_ARGS{
		flags: C.CKF_OS_LOCKING_OK,
	}
	if err := isOk("C_Initialize", C.ck_initialize(p, &args)); err != nil {
		C.dlclose(mod)
		return nil, err
	}

	var info C.CK_INFO
	if err := isOk("C_GetInfo", C.ck_get_info(p, &info)); err != nil {
		C.dlclose(mod)
		return nil, err
	}

	return &Module{
		mod:     mod,
		fl:      p,
		version: info.cryptokiVersion,
		info: Info{
			Manufacturer: toString(info.manufacturerID[:]),
			Version: Version{
				Major: uint8(info.libraryVersion.major),
				Minor: uint8(info.libraryVersion.minor),
			},
		},
	}, nil
}

// Close finalizes the module and releases any resources associated with the
// shared library.
func (m *Module) Close() error {
	if err := isOk("C_Finalize", C.ck_finalize(m.fl)); err != nil {
		return err
	}
	if C.dlclose(m.mod) != 0 {
		return fmt.Errorf("pkcs11: dlclose error: %s", C.GoString(C.dlerror()))
	}
	return nil
}

func ckStringPadded(b []C.CK_UTF8CHAR, s string) bool {
	if len(s) > len(b) {
		return false
	}
	for i := range b {
		if i < len(s) {
			b[i] = C.CK_UTF8CHAR(s[i])
		} else {
			b[i] = C.CK_UTF8CHAR(' ')
		}
	}
	return true
}

func ckString(s string) []C.CK_UTF8CHAR {
	b := make([]C.CK_UTF8CHAR, len(s))
	for i, c := range []byte(s) {
		b[i] = C.CK_UTF8CHAR(c)
	}
	return b
}

// SlotInitialize configures a slot object. Internally this calls C_InitToken.
//
// Different modules have different restrictions on the PIN value, and some
// may refuse an empty PIN.
func (m *Module) SlotInitialize(slotID uint32, label, adminPIN string) error {
	var cLabel [32]C.CK_UTF8CHAR
	if !ckStringPadded(cLabel[:], label) {
		return fmt.Errorf("pkcs11: label too long")
	}

	cPIN := ckString(adminPIN)
	cPINLen := C.CK_ULONG(len(cPIN))

	var cPINPtr C.CK_UTF8CHAR_PTR
	if len(adminPIN) > 0 {
		cPINPtr = &cPIN[0]
	}

	rv := C.ck_init_token(
		m.fl,
		C.CK_SLOT_ID(slotID),
		cPINPtr,
		cPINLen,
		&cLabel[0],
	)
	if err := isOk("C_InitToken", rv); err != nil {
		return err
	}

	return nil
}

// SlotIDs returns the IDs of all slots associated with this module, including
// ones that haven't been initalized.
func (m *Module) SlotIDs() ([]uint32, error) {
	var n C.CK_ULONG
	rv := C.ck_get_slot_list(m.fl, nil, &n)
	if err := isOk("C_GetSlotList", rv); err != nil {
		return nil, err
	}

	l := make([]C.CK_SLOT_ID, int(n))
	rv = C.ck_get_slot_list(m.fl, &l[0], &n)
	if err := isOk("C_GetSlotList", rv); err != nil {
		return nil, err
	}
	if int(n) > len(l) {
		return nil, fmt.Errorf("pkcs11: C_GetSlotList returned too many elements, got %d, want %d", int(n), len(l))
	}
	l = l[:int(n)]

	ids := make([]uint32, len(l))
	for i, id := range l {
		ids[i] = uint32(id)
	}
	return ids, nil
}

// Version holds a major and minor version.
type Version struct {
	Major uint8
	Minor uint8
}

// Info holds global information about the module.
type Info struct {
	// Manufacturer of the implementation. When multiple PKCS #11 devices are
	// present this is used to differentiate devices.
	Manufacturer string
	// Version of the module.
	Version Version
	// Human readable description of the module.
	Description string
}

// SlotInfo holds information about the slot and underlying token.
type SlotInfo struct {
	Label  string
	Model  string
	Serial string

	Description string
}

func toString(b []C.uchar) string {
	lastIndex := len(b)
	for i := len(b); i > 0; i-- {
		if b[i-1] != C.uchar(' ') {
			break
		}
		lastIndex = i - 1
	}

	var sb strings.Builder
	for _, c := range b[:lastIndex] {
		sb.WriteByte(byte(c))
	}
	return sb.String()
}

// Info returns additional information about the module.
func (m *Module) Info() Info {
	return m.info
}

// SlotInfo queries for information about the slot, such as the label.
func (m *Module) SlotInfo(id uint32) (*SlotInfo, error) {
	var (
		cSlotInfo  C.CK_SLOT_INFO
		cTokenInfo C.CK_TOKEN_INFO
		slotID     = C.CK_SLOT_ID(id)
	)
	rv := C.ck_get_slot_info(m.fl, slotID, &cSlotInfo)
	if err := isOk("C_GetSlotInfo", rv); err != nil {
		return nil, err
	}
	info := SlotInfo{
		Description: toString(cSlotInfo.slotDescription[:]),
	}
	if (cSlotInfo.flags & C.CKF_TOKEN_PRESENT) == 0 {
		return &info, nil
	}

	rv = C.ck_get_token_info(m.fl, slotID, &cTokenInfo)
	if err := isOk("C_GetTokenInfo", rv); err != nil {
		return nil, err
	}
	info.Label = toString(cTokenInfo.label[:])
	info.Model = toString(cTokenInfo.model[:])
	info.Serial = toString(cTokenInfo.serialNumber[:])
	return &info, nil
}

// Slot represents a session to a slot.
//
// A slot holds a listable set of objects, such as certificates and
// cryptographic keys.
type Slot struct {
	fl C.CK_FUNCTION_LIST_PTR
	h  C.CK_SESSION_HANDLE
}

// SlotOption is a configuration option for the slot session.
type SlotOption interface {
	isSlotOption()
}

type slotOptionFlag C.CK_FLAGS

func (f slotOptionFlag) isSlotOption() {}

// SlotReadWrite indicates that the slot should be opened with write capabilities,
// such as generating keys or importing certificates.
//
// By default, sessions can access objects and perform signing requests.
func SlotReadWrite() SlotOption {
	return slotOptionFlag(C.CKF_RW_SESSION)
}

// Slot creates a session with the given slot, by default read-only. Users
// must call Close to release the session.
//
// SlotOption values can be provided to change the behavior of the slot
// session.
//
// The returned Slot's behavior is undefined once the Module is closed.
func (m *Module) Slot(id uint32, opts ...SlotOption) (*Slot, error) {
	var (
		h      C.CK_SESSION_HANDLE
		slotID = C.CK_SLOT_ID(id)
		// "For legacy reasons, the CKF_SERIAL_SESSION bit MUST always be set".
		//
		// http://docs.oasis-open.org/pkcs11/pkcs11-base/v2.40/os/pkcs11-base-v2.40-os.html#_Toc416959742
		flags C.CK_FLAGS = C.CKF_SERIAL_SESSION
	)

	for _, o := range opts {
		switch o := o.(type) {
		case slotOptionFlag:
			flags = flags | C.CK_FLAGS(o)
		}
	}

	rv := C.ck_open_session(m.fl, slotID, flags, &h)
	if err := isOk("C_OpenSession", rv); err != nil {
		return nil, err
	}

	return &Slot{fl: m.fl, h: h}, nil
}

// Close releases the slot session.
func (s *Slot) Close() error {
	return isOk("C_CloseSession", C.ck_close_session(s.fl, s.h))
}

// TODO(ericchiang): merge with SlotInitialize.
func (s *Slot) InitPIN(pin string) error {
	if pin == "" {
		return fmt.Errorf("invalid pin")
	}
	cPIN := ckString(pin)
	cPINLen := C.CK_ULONG(len(cPIN))
	return isOk("C_InitPIN", C.ck_init_pin(s.fl, s.h, &cPIN[0], cPINLen))
}

func (s *Slot) Logout() error {
	return isOk("C_Logout", C.ck_logout(s.fl, s.h))
}

func (s *Slot) Login(pin string) error {
	// TODO(ericchiang): check for CKR_USER_ALREADY_LOGGED_IN and auto logout.
	if pin == "" {
		return fmt.Errorf("invalid pin")
	}
	cPIN := ckString(pin)
	cPINLen := C.CK_ULONG(len(cPIN))
	return isOk("C_Login", C.ck_login(s.fl, s.h, C.CKU_USER, &cPIN[0], cPINLen))
}

func (s *Slot) LoginAdmin(adminPIN string) error {
	// TODO(ericchiang): maybe run commands, detect CKR_USER_NOT_LOGGED_IN, then
	// automatically login?
	if adminPIN == "" {
		return fmt.Errorf("invalid admin pin")
	}
	cPIN := ckString(adminPIN)
	cPINLen := C.CK_ULONG(len(cPIN))
	return isOk("C_Login", C.ck_login(s.fl, s.h, C.CKU_SO, &cPIN[0], cPINLen))
}

type GenerateOption interface {
	isGenerateOption()
}

// GenerateCDSA is a generation option that creates an ECDSA private key on the
// slot.
type GenerateECDSA struct {
	Curve ECDSACurve
}

func (g GenerateECDSA) isGenerateOption() {}

type ECDSACurve int

const (
	P256 ECDSACurve = iota + 1
	P384
	P521
)

// https://datatracker.ietf.org/doc/html/rfc5480#section-2.1.1.1

// Generate a private key on the slot, creating associated private and public
// key objects.
//
// Generate accepts GenerateECDSA or GenerateRSA.
//
//		o := pkcs11.GenerateECDSA{
//			Curve: pkcs11.PS256,
//		}
//		priv, err := m.Generate(o)
//
func (s *Slot) Generate(opts GenerateOption) (crypto.PrivateKey, error) {
	switch o := opts.(type) {
	case GenerateECDSA:
		return s.generateECDSA(o)
	default:
		return nil, fmt.Errorf("unreachable")
	}
}

// generateECDSA implements the CKM_ECDSA_KEY_PAIR_GEN mechanism.
//
// http://docs.oasis-open.org/pkcs11/pkcs11-base/v2.40/os/pkcs11-base-v2.40-os.html#_Toc416959719
// https://datatracker.ietf.org/doc/html/rfc5480#section-2.1.1.1
// http://docs.oasis-open.org/pkcs11/pkcs11-curr/v2.40/os/pkcs11-curr-v2.40-os.html#_Toc416960014
func (s *Slot) generateECDSA(o GenerateECDSA) (crypto.PrivateKey, error) {
	var (
		mechanism = C.CK_MECHANISM{
			mechanism: C.CKM_EC_KEY_PAIR_GEN,
		}
		pubH  C.CK_OBJECT_HANDLE
		privH C.CK_OBJECT_HANDLE
	)

	// https://datatracker.ietf.org/doc/html/rfc5480#section-2.1.1.1
	var oid asn1.ObjectIdentifier
	switch o.Curve {
	case P256:
		oid = asn1.ObjectIdentifier{1, 2, 840, 10045, 3, 1, 7}
	case P384:
		oid = asn1.ObjectIdentifier{1, 3, 132, 0, 34}
	case P521:
		oid = asn1.ObjectIdentifier{1, 3, 132, 0, 35}
	default:
		return nil, fmt.Errorf("unsupported ECDSA curve")
	}

	oidASN1, err := asn1.Marshal(oid)
	if err != nil {
		return nil, fmt.Errorf("marshal algorithm identifier: %v", err)
	}

	// When passing a struct or array to C, that value can't refer to Go
	// memory. Allocate all attribute values in C rather than in Go.
	cOID := (C.CK_VOID_PTR)(C.CBytes(oidASN1))
	defer C.free(unsafe.Pointer(cOID))

	cTrue := (C.CK_VOID_PTR)(C.malloc(C.sizeof_CK_BBOOL))
	cFalse := (C.CK_VOID_PTR)(C.malloc(C.sizeof_CK_BBOOL))
	defer C.free(unsafe.Pointer(cTrue))
	defer C.free(unsafe.Pointer(cFalse))
	*((*C.CK_BBOOL)(cTrue)) = C.CK_TRUE
	*((*C.CK_BBOOL)(cFalse)) = C.CK_FALSE

	privTmpl := []C.CK_ATTRIBUTE{
		{C.CKA_PRIVATE, cTrue, C.CK_ULONG(C.sizeof_CK_BBOOL)},
		{C.CKA_SENSITIVE, cTrue, C.CK_ULONG(C.sizeof_CK_BBOOL)},
		{C.CKA_SIGN, cTrue, C.CK_ULONG(C.sizeof_CK_BBOOL)},
	}

	pubTmpl := []C.CK_ATTRIBUTE{
		{C.CKA_EC_PARAMS, cOID, C.CK_ULONG(len(oidASN1))},
		{C.CKA_VERIFY, cTrue, C.CK_ULONG(C.sizeof_CK_BBOOL)},
	}
	rv := C.ck_generate_key_pair(
		s.fl, s.h, &mechanism,
		&pubTmpl[0], C.CK_ULONG(len(pubTmpl)),
		&privTmpl[0], C.CK_ULONG(len(privTmpl)),
		&pubH, &privH,
	)

	if err := isOk("C_GenerateKeyPair", rv); err != nil {
		return nil, err
	}
	return nil, nil
}
