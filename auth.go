package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
)

const cookieName = "session"
const usertextDb = "users.json"

func loginRequest(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case "GET":
		displayLogin(w, req)
	case "POST":
		doLogin(w, req)
	default:
		w.WriteHeader(400)
		fmt.Fprintf(w, "Unsupported method")
	}
}

func displayLogin(w http.ResponseWriter, req *http.Request) {
	log.Println(getRealIPAddress(req), "has requested auth page: ", req.Method)

	err := renderTemplate(w, "login.html", nil)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "Something went wrong")
		log.Printf("Use has triggered an error %s\n", err)
		return
	}

}

func doLogin(w http.ResponseWriter, req *http.Request) {
	log.Println(getRealIPAddress(req), "has auth tried to auth")

	if err := req.ParseForm(); err != nil {
		w.WriteHeader(500)
		fmt.Fprintf(w, "Something has gone wrong")
		return
	}

	username, password := req.FormValue("username"), req.FormValue("password")

	err := VerifyUser(username, password)
	if err != nil {
		log.Println(getRealIPAddress(req), "has failed to auth")

		http.Redirect(w, req, "/auth#Error:Invalid username and password combination", http.StatusFound)
		return
	}

	log.Println(getRealIPAddress(req), "has authed")

	mintCookie(w, username)

	http.Redirect(w, req, "/", http.StatusFound)
}

func generateFromPassword(password string) (encodedHash string, err error) {
	// Generate a cryptographically secure random salt.
	salt := randomData(16)

	// Pass the plaintext password, salt and parameters to the argon2.IDKey
	// function. This will generate a hash of the password using the Argon2id
	// variant.
	hash := argon2.IDKey([]byte(password), salt, 1, 64*1024, 4, 32)

	// Base64 encode the salt and hashed password.
	b64Salt := base64.RawStdEncoding.EncodeToString(salt)
	b64Hash := base64.RawStdEncoding.EncodeToString(hash)

	// Return a string using the standard encoded hash representation.
	encodedHash = fmt.Sprintf("%s$%s", b64Salt, b64Hash)

	return encodedHash, nil
}

func comparePasswordAndHash(password, encodedHash string) (err error) {
	// Extract the parameters, salt and derived key from the encoded password
	// hash.

	vals := strings.Split(encodedHash, "$")

	if len(vals) != 2 {
		return errors.New("Hash did not match expected format")
	}

	salt, err := base64.RawStdEncoding.Strict().DecodeString(vals[0])
	if err != nil {
		return err
	}

	hash, err := base64.RawStdEncoding.Strict().DecodeString(vals[1])
	if err != nil {
		return err
	}

	// Derive the key from the other password using the same parameters.
	otherHash := argon2.IDKey([]byte(password), salt, 1, 64*1024, 4, 32)

	// Check that the contents of the hashed passwords are identical. Note
	// that we are using the subtle.ConstantTimeCompare() function for this
	// to help prevent timing attacks.
	if subtle.ConstantTimeCompare(hash, otherHash) == 1 {
		return nil
	}
	return errors.New("Hashes do not match")
}

func getUsersDb() (users map[string]string, err error) {

	databaseLocation := filepath.Join(executableDirectory, usertextDb)
	if _, err := os.Stat(databaseLocation); errors.Is(err, os.ErrNotExist) {
		log.Println("[WARN] Users database didnt exist to read")
		return make(map[string]string), nil
	}

	var text []byte
	text, err = ioutil.ReadFile(databaseLocation)
	if err != nil {
		return
	}

	err = json.Unmarshal(text, &users)
	if err != nil {
		return
	}

	return
}

func storeUsersDb(db map[string]string) error {
	output, err := json.Marshal(&db)
	if err != nil {
		return err
	}

	return ioutil.WriteFile(filepath.Join(executableDirectory, usertextDb), output, 0700)
}

func AddUser(username, password string) error {
	users, err := getUsersDb()
	if err != nil {
		return err
	}

	if _, ok := users[username]; ok {
		fmt.Println("[WARN] User", username, "already exists, this will reset their password")
	}

	hash, err := generateFromPassword(password)
	if err != nil {
		return err
	}

	users[username] = hash

	return storeUsersDb(users)
}

func VerifyUser(username, password string) error {
	users, err := getUsersDb()
	if err != nil {
		return err
	}

	//Doing this regardless if user exists or not to stop timing attacks discovering users
	hash := users[username]
	return comparePasswordAndHash(password, hash)
}

func RemoveUser(username string) error {
	users, err := getUsersDb()
	if err != nil {
		return err
	}

	delete(users, username)

	return storeUsersDb(users)
}

func mintCookie(w http.ResponseWriter, username string) {

	nonce := make([]byte, siteCookieEncryption.NonceSize(), siteCookieEncryption.NonceSize()+len(username)+siteCookieEncryption.Overhead())
	if _, err := rand.Read(nonce); err != nil {
		panic(err)
	}

	// Encrypt the message and append the ciphertext to the nonce.
	encryptedMsg := siteCookieEncryption.Seal(nonce, nonce, []byte(username), nil)

	c := http.Cookie{
		Name:     cookieName,
		Value:    hex.EncodeToString(encryptedMsg),
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		HttpOnly: true,
		Expires:  time.Now().Add(7 * 24 * time.Hour),
	}

	http.SetCookie(w, &c)

}

func verifyCookie(req *http.Request) error {
	sessionCookie, err := req.Cookie(cookieName)
	if err != nil {
		return err
	}

	decodedCiphertext, err := hex.DecodeString(sessionCookie.Value)
	if err != nil {
		return err
	}

	if len(decodedCiphertext) < siteCookieEncryption.NonceSize() {
		return errors.New("ciphertext too short")
	}

	// Split nonce and ciphertext.
	nonce, ciphertext := decodedCiphertext[:siteCookieEncryption.NonceSize()], decodedCiphertext[siteCookieEncryption.NonceSize():]

	// Decrypt the message and check it wasn't tampered with.
	plaintext, err := siteCookieEncryption.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return err
	}

	log.Printf("[%s] %s\n", string(plaintext), req.URL)

	return nil
}
