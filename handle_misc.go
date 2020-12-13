// Package ftpserver provides all the tools to build your own FTP server: The core library and the driver.
package ftpserver

import (
	"bufio"
	"crypto/md5"  //nolint:gosec
	"crypto/sha1" //nolint:gosec
	"crypto/sha256"
	"crypto/sha512"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

var errUnknowHash = errors.New("unknown hash algorithm")

func (c *clientHandler) handleAUTH() error {
	if tlsConfig, err := c.server.driver.GetTLSConfig(); err == nil {
		c.writeMessage(StatusAuthAccepted, "AUTH command ok. Expecting TLS Negotiation.")
		c.conn = tls.Server(c.conn, tlsConfig)
		c.reader = bufio.NewReader(c.conn)
		c.writer = bufio.NewWriter(c.conn)
		c.controlTLS = true
	} else {
		c.writeMessage(StatusActionNotTaken, fmt.Sprintf("Cannot get a TLS config: %v", err))
	}

	return nil
}

func (c *clientHandler) handlePROT() error {
	// P for Private, C for Clear
	c.transferTLS = c.param == "P"
	c.writeMessage(StatusOK, "OK")

	return nil
}

func (c *clientHandler) handlePBSZ() error {
	c.writeMessage(StatusOK, "Whatever")
	return nil
}

func (c *clientHandler) handleSYST() error {
	c.writeMessage(StatusSystemType, "UNIX Type: L8")
	return nil
}

func (c *clientHandler) handleSTAT() error {
	if c.param == "" { // Without a file, it's the server stat
		return c.handleSTATServer()
	}

	// With a file/dir it's the file or the dir's files stat
	return c.handleSTATFile()
}

func (c *clientHandler) handleSITE() error {
	if c.server.settings.DisableSite {
		c.writeMessage(StatusSyntaxErrorNotRecognised, "SITE support is disabled")
		return nil
	}

	spl := strings.SplitN(c.param, " ", 2)
	if len(spl) > 1 {
		switch strings.ToUpper(spl[0]) {
		case "CHMOD":
			c.handleCHMOD(spl[1])
			return nil
		case "CHOWN":
			c.handleCHOWN(spl[1])
			return nil
		case "SYMLINK":
			c.handleSYMLINK(spl[1])
			return nil
		}
	}

	c.writeMessage(StatusSyntaxErrorNotRecognised, "Not understood SITE subcommand")

	return nil
}

func (c *clientHandler) handleSTATServer() error {
	defer c.multilineAnswer(StatusFileStatus, "Server status")()

	duration := time.Now().UTC().Sub(c.connectedAt)
	duration -= duration % time.Second
	c.writeLine(fmt.Sprintf(
		"Connected to %s from %s for %s",
		c.server.settings.ListenAddr,
		c.conn.RemoteAddr(),
		duration,
	))

	if c.user != "" {
		c.writeLine(fmt.Sprintf("Logged in as %s", c.user))
	} else {
		c.writeLine("Not logged in yet")
	}

	c.writeLine(c.server.settings.Banner)

	return nil
}

func (c *clientHandler) handleOPTS() error {
	args := strings.SplitN(c.param, " ", 2)
	if strings.EqualFold(args[0], "UTF8") {
		c.writeMessage(StatusOK, "I'm in UTF8 only anyway")
		return nil
	}

	if strings.EqualFold(args[0], "HASH") && c.server.settings.EnableHASH {
		hashMapping := getHashMapping()

		if len(args) > 1 {
			// try to change the current hash algorithm to the requested one
			if value, ok := hashMapping[args[1]]; ok {
				c.selectedHashAlgo = value
				c.writeMessage(StatusOK, args[1])
			} else {
				c.writeMessage(StatusSyntaxErrorParameters, "Unknown algorithm, current selection not changed")
			}

			return nil
		}
		// return the current hash algorithm
		var currentHash string

		for k, v := range hashMapping {
			if v == c.selectedHashAlgo {
				currentHash = k
			}
		}

		c.writeMessage(StatusOK, currentHash)

		return nil
	}

	c.writeMessage(StatusSyntaxErrorNotRecognised, "Don't know this option")

	return nil
}

func (c *clientHandler) handleNOOP() error {
	c.writeMessage(StatusOK, "OK")
	return nil
}

func (c *clientHandler) handleCLNT() error {
	c.clnt = c.param
	c.writeMessage(StatusOK, "Good to know")

	return nil
}

func (c *clientHandler) handleFEAT() error {
	c.writeLine(fmt.Sprintf("%d- These are my features", StatusSystemStatus))
	defer c.writeMessage(StatusSystemStatus, "end")

	features := []string{
		"CLNT",
		"UTF8",
		"SIZE",
		"MDTM",
		"REST STREAM",
	}

	if !c.server.settings.DisableMLSD {
		features = append(features, "MLSD")
	}

	if !c.server.settings.DisableMLST {
		features = append(features, "MLST")
	}

	if !c.server.settings.DisableMFMT {
		features = append(features, "MFMT")
	}

	// This code made me think about adding this: https://github.com/stianstr/ftpserver/commit/387f2ba
	if tlsConfig, err := c.server.driver.GetTLSConfig(); tlsConfig != nil && err == nil {
		features = append(features, "AUTH TLS")
	}

	if c.server.settings.EnableHASH {
		var hashLine strings.Builder

		nonStandardHashImpl := []string{"XCRC", "MD5", "XMD5", "XSHA", "XSHA1", "XSHA256", "XSHA512"}
		hashMapping := getHashMapping()

		for k, v := range hashMapping {
			hashLine.WriteString(k)

			if v == c.selectedHashAlgo {
				hashLine.WriteString("*")
			}

			hashLine.WriteString(";")
		}

		features = append(features, hashLine.String())
		features = append(features, nonStandardHashImpl...)
	}

	for _, f := range features {
		c.writeLine(" " + f)
	}

	return nil
}

func (c *clientHandler) handleHASH() error {
	return c.handleGenericHash(c.selectedHashAlgo, false)
}

func (c *clientHandler) handleCRC32() error {
	return c.handleGenericHash(HASHAlgoCRC32, true)
}

func (c *clientHandler) handleMD5() error {
	return c.handleGenericHash(HASHAlgoMD5, true)
}

func (c *clientHandler) handleSHA1() error {
	return c.handleGenericHash(HASHAlgoSHA1, true)
}

func (c *clientHandler) handleSHA256() error {
	return c.handleGenericHash(HASHAlgoSHA256, true)
}

func (c *clientHandler) handleSHA512() error {
	return c.handleGenericHash(HASHAlgoSHA512, true)
}

func (c *clientHandler) handleGenericHash(algo HASHAlgo, isCustomMode bool) error {
	args := strings.SplitN(c.param, " ", 3)
	info, err := c.driver.Stat(args[0])

	if err != nil {
		c.writeMessage(StatusActionNotTaken, fmt.Sprintf("%v: %v", c.param, err))
		return nil
	}

	if !info.Mode().IsRegular() {
		c.writeMessage(StatusActionNotTakenNoFile, fmt.Sprintf("%v is not a regular file", c.param))
		return nil
	}

	start := int64(0)
	end := info.Size()

	if isCustomMode {
		// for custom command the range can be specified in this way:
		// XSHA1 <file> <start> <end>
		if len(args) > 1 {
			start, err = strconv.ParseInt(args[1], 10, 64)
			if err != nil {
				c.writeMessage(StatusSyntaxErrorParameters, fmt.Sprintf("invalid start offset %v: %v", args[1], err))
				return nil
			}
		}

		if len(args) > 2 {
			end, err = strconv.ParseInt(args[2], 10, 64)
			if err != nil {
				c.writeMessage(StatusSyntaxErrorParameters, fmt.Sprintf("invalid end offset %v2: %v", args[2], err))
				return nil
			}
		}
	}
	// to support partial hash also for the HASH command we should implement RANG too,
	// but this apply also to uploads/downloads and so complicat the things, we'll add
	// this support in future improvements

	result, err := c.computeHashForFile(c.absPath(args[0]), algo, start, end)
	if err != nil {
		c.writeMessage(StatusActionNotTaken, fmt.Sprintf("%v: %v", args[0], err))
		return nil
	}

	hashMapping := getHashMapping()
	hashName := ""

	for k, v := range hashMapping {
		if v == algo {
			hashName = k
		}
	}

	firstLine := fmt.Sprintf("Computing %v digest", hashName)

	if isCustomMode {
		c.writeMessage(StatusFileOK, fmt.Sprintf("%v\r\n%v", firstLine, result))
		return nil
	}

	response := fmt.Sprintf("%v\r\n%v %v-%v %v %v", firstLine, hashName, start, end, result, args[0])
	c.writeMessage(StatusFileStatus, response)

	return nil
}

func (c *clientHandler) handleTYPE() error {
	switch c.param {
	case "I":
		c.writeMessage(StatusOK, "Type set to binary")
	case "A":
		c.writeMessage(StatusOK, "ASCII isn't properly supported: https://github.com/fclairamb/ftpserverlib/issues/86")
	default:
		c.writeMessage(StatusSyntaxErrorNotRecognised, "Not understood")
	}

	return nil
}

func (c *clientHandler) handleQUIT() error {
	c.writeMessage(StatusClosingControlConn, "Goodbye")
	c.disconnect()
	c.reader = nil

	return nil
}

func (c *clientHandler) computeHashForFile(filePath string, algo HASHAlgo, start, end int64) (string, error) {
	var h hash.Hash
	var file FileTransfer
	var err error

	switch algo {
	case HASHAlgoCRC32:
		h = crc32.NewIEEE()
	case HASHAlgoMD5:
		h = md5.New() //nolint:gosec
	case HASHAlgoSHA1:
		h = sha1.New() //nolint:gosec
	case HASHAlgoSHA256:
		h = sha256.New()
	case HASHAlgoSHA512:
		h = sha512.New()
	default:
		return "", errUnknowHash
	}

	if fileTransfer, ok := c.driver.(ClientDriverExtentionFileTransfer); ok {
		file, err = fileTransfer.GetHandle(filePath, os.O_RDONLY, start)
	} else {
		file, err = c.driver.OpenFile(filePath, os.O_RDONLY, os.ModePerm)
	}

	if err != nil {
		return "", err
	}

	if start > 0 {
		_, err = file.Seek(start, io.SeekStart)
		if err != nil {
			return "", err
		}
	}

	_, err = io.CopyN(h, file, end-start)
	defer file.Close() //nolint:errcheck // we ignore close error here

	if err != nil && err != io.EOF {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}
