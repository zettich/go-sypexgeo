// Package sypexgeo to access data from Sypex Geo IP database files,
// accepts only SypexGeo 2.2 databases
package sypexgeo

import (
	"errors"
	"fmt"
	"io/ioutil"
	"strconv"
	"strings"
)

type (
	dbSlices struct {
		BIndex    []uint32 // byte index
		MIndex    []uint32 // main index
		DB        []byte
		Regions   []byte
		Cities    []byte
		Countries []byte
	}
	finder struct {
		BLen       uint32   // byte index length
		MLen       uint32   // main index length
		Blocks     uint32   // blocks in index item
		DBItems    uint32   // number of IP in the database
		IDLen      uint32   // size of ID-block 1-city, 3-country
		BlockLen   uint32   // size of DB block = IDLen+3
		PackLen    uint32   // size of pack info
		MaxRegion  uint32   // max length of the region record
		MaxCity    uint32   // max length of the city record
		MaxCountry uint32   // max length of the country record
		CountryLen uint32   // size of country catalog
		Pack       []string // pack info
		S          dbSlices
	}
	// SxGEO main object
	SxGEO struct {
		finder  finder
		Version float32
		Updated uint32
	}
	// Country struct
	Country struct {
		ID         uint8
		ISO        string
		TimeZone   string
		NameEn     string
		NameRu     string
		CapitalEn  string
		CapitalRu  string
		CapitalId  uint8
		Continent  string
		Neighbours string
		Currency   string
		VK         uint8
		Phone      string
		Lon        float32
		Lat        float32
	}
	// Region struct
	Region struct {
		ID       uint32
		ISO      string
		TimeZone string
		OKATO    string
		VK       uint8
		NameEn   string
		NameRu   string
	}
	// City struct
	City struct {
		ID     uint32
		NameEn string
		NameRu string
		OKATO  string
		VK     uint8
		Lon    float32
		Lat    float32
	}
	// Result of geoip lookup
	Result struct {
		Country Country
		Region  Region
		City    City
	}
)

/*func test() {
	a := &SxGEO{}
	a.Get
}*/

func (f *finder) getLocationOffset(IP string) (uint32, error) {
	firstByte, err := getIPByte(IP, 0)
	if err != nil {
		return 0, err
	}
	IPn := uint32(ipToN(IP))
	if firstByte == 0 || firstByte == 10 || firstByte == 127 || uint32(firstByte) >= f.BLen || IPn == 0 {
		return 0, errors.New("IP out of range")
	}

	var min, max uint32
	minIndex, maxIndex := f.S.BIndex[firstByte-1], f.S.BIndex[firstByte]

	if maxIndex-minIndex > f.Blocks {
		part := f.searchIdx(IPn, minIndex/f.Blocks, maxIndex/f.Blocks-1)
		max = f.DBItems
		if part > 0 {
			min = part * f.Blocks
		}
		if part <= f.MLen {
			max = (part + 1) * f.Blocks
		}
		min, max = max32(min, minIndex), min32(max, maxIndex)
	} else {
		min, max = minIndex, maxIndex
	}
	return f.searchDb(IPn, min, max), nil
}

func (f *finder) searchDb(IPn, min, max uint32) uint32 {
	if max-min > 1 {
		IPn &= 0x00FFFFFF

		for max-min > 8 {
			offset := (min + max) >> 1
			// if IPn > substr(str, offset*f.block_len, 3) {
			if IPn > sliceUint32(f.S.DB, offset*f.BlockLen, 3) {
				min = offset
			} else {
				max = offset
			}
		}

		for IPn >= sliceUint32(f.S.DB, min*f.BlockLen, 3) {
			min++
			if min >= max {
				break
			}
		}
	} else {
		min++
	}

	return sliceUint32(f.S.DB, min*f.BlockLen-f.IDLen, f.IDLen)
}

func (f *finder) searchIdx(IPn, min, max uint32) uint32 {
	var offset uint32
	if max < min {
		max, min = min, max
	}
	for max-min > 8 {
		offset = (min + max) >> 1
		if IPn > uint32(f.S.MIndex[offset]) {
			min = offset
		} else {
			max = offset
		}
	}
	for IPn > uint32(f.S.MIndex[min]) {
		min++
		if min > max {
			break
		}
	}
	return min
}

func (f *finder) unpack(seek, uType uint32) (map[string]interface{}, error) {
	var bs []byte
	var maxLen uint32
	ret := obj()

	if int(uType+1) > len(f.Pack) {
		return obj(), errors.New("Pack method not found")
	}

	switch uType {
	case 0:
		bs = f.S.Cities
		maxLen = f.MaxCountry
	case 1:
		bs = f.S.Regions
		maxLen = f.MaxRegion
	case 2:
		bs = f.S.Cities
		maxLen = f.MaxCity
	}

	limit := int(seek) + int(maxLen)
	if limit > cap(bs) {
		limit = cap(bs)
	}

	raw := bs[seek:limit]

	var cursor int
	for _, el := range strings.Split(f.Pack[uType], "/") {
		cmd := strings.Split(el, ":")

		switch string(cmd[0][0]) {
		case "T":
			ret[cmd[1]] = raw[cursor]
			cursor++
		case "M":
			ret[cmd[1]] = sliceUint32L(raw, cursor, 3)
			cursor += 3
		case "S":
			ret[cmd[1]] = readUint16L(raw, cursor)
			cursor += 2
		case "b":
			ret[cmd[1]] = readString(raw, cursor)
			cursor += len(ret[cmd[1]].(string)) + 1
		case "c":
			if len(cmd[0]) > 1 {
				ln, _ := strconv.Atoi(string(cmd[0][1:]))
				ret[cmd[1]] = string(raw[cursor : cursor+ln])
				cursor += ln
			}
		case "N":
			if len(cmd[0]) > 1 {
				coma, _ := strconv.Atoi(string(cmd[0][1:]))
				ret[cmd[1]] = readN32L(raw, cursor, coma)
				cursor += 4
			}
		case "n":
			if len(cmd[0]) > 1 {
				coma, _ := strconv.Atoi(string(cmd[0][1:]))
				ret[cmd[1]] = readN16L(raw, cursor, coma)
				cursor += 2
			}
		}
	}
	return ret, nil
}

func (f *finder) parseToStruct(seek uint32, full bool) (Result, error) {
	ret := Result{}
	result, err := f.parseCity(seek, full)
	if err == nil {
		if countryIfce, exists := result["country"]; exists {
			country, _ := countryIfce.(map[string]interface{})
			if f, ok := country["id"]; ok {
				ret.Country.ID, _ = f.(uint8)
			}
			if f, ok := country["name_en"]; ok {
				ret.Country.NameEn, _ = f.(string)
			}
			if f, ok := country["name_ru"]; ok {
				ret.Country.NameRu, _ = f.(string)
			}
			if f, ok := country["lat"]; ok {
				ret.Country.Lat, _ = f.(float32)
			}
			if f, ok := country["lon"]; ok {
				ret.Country.Lon, _ = f.(float32)
			}
			if f, ok := country["iso"]; ok {
				ret.Country.ISO, _ = f.(string)
			}
			if f, ok := country["timezone"]; ok {
				ret.Country.TimeZone, _ = f.(string)
			}
			if f, ok := country["cur_code"]; ok {
				ret.Country.Currency, _ = f.(string)
			}
			if f, ok := country["continent"]; ok {
				ret.Country.Continent, _ = f.(string)
			}
			if f, ok := country["vk"]; ok {
				ret.Country.VK, _ = f.(uint8)
			}
			if f, ok := country["capital_en"]; ok {
				ret.Country.CapitalEn, _ = f.(string)
			}
			if f, ok := country["capital_ru"]; ok {
				ret.Country.CapitalRu, _ = f.(string)
			}
			if f, ok := country["capital_id"]; ok {
				ret.Country.CapitalId, _ = f.(uint8)
			}
			if f, ok := country["neighbours"]; ok {
				ret.Country.Neighbours, _ = f.(string)
			}
			if f, ok := country["phone"]; ok {
				ret.Country.Phone, _ = f.(string)
			}
		}
		if cregionIfce, exists := result["region"]; exists {
			region, _ := cregionIfce.(map[string]interface{})
			if f, ok := region["id"]; ok {
				ret.Region.ID, _ = f.(uint32)
			}
			if f, ok := region["name_en"]; ok {
				ret.Region.NameEn, _ = f.(string)
			}
			if f, ok := region["name_ru"]; ok {
				ret.Region.NameRu, _ = f.(string)
			}
			if f, ok := region["iso"]; ok {
				ret.Region.ISO, _ = f.(string)
			}
			if f, ok := region["timezone"]; ok {
				ret.Region.TimeZone, _ = f.(string)
			}
			if f, ok := region["okato"]; ok {
				ret.Region.OKATO, _ = f.(string)
			}
			if f, ok := region["vk"]; ok {
				ret.Region.VK, _ = f.(uint8)
			}
		}
		if cityIfce, exists := result["city"]; exists {
			city, _ := cityIfce.(map[string]interface{})
			if f, ok := city["id"]; ok {
				ret.City.ID, _ = f.(uint32)
			}
			if f, ok := city["name_en"]; ok {
				ret.City.NameEn, _ = f.(string)
			}
			if f, ok := city["name_ru"]; ok {
				ret.City.NameRu, _ = f.(string)
			}
			if f, ok := city["okato"]; ok {
				ret.City.OKATO, _ = f.(string)
			}
			if f, ok := city["vk"]; ok {
				ret.City.VK, _ = f.(uint8)
			}
			if f, ok := city["lat"]; ok {
				ret.City.Lat, _ = f.(float32)
			}
			if f, ok := city["lon"]; ok {
				ret.City.Lon, _ = f.(float32)
			}
		}
	}
	return ret, err
}

func (f *finder) parseCity(seek uint32, full bool) (map[string]interface{}, error) {
	if f.PackLen == 0 {
		return obj(), errors.New("Pack methods not found")
	}
	country, city, region := obj(), obj(), obj()
	var err error
	onlyCountry := false

	if seek < f.CountryLen {
		country, err = f.unpack(seek, 0)
		if country["id"] == nil || country["id"].(uint8) == 0 {
			return obj(), errors.New("IP out of range")
		}
		city = map[string]interface{}{
			"id":      0,
			"lat":     country["lat"],
			"lon":     country["lon"],
			"name_en": "",
			"name_ru": "",
		}
		onlyCountry = true
	} else {
		city, err = f.unpack(seek, 2)
		country = map[string]interface{}{
			"id":      city["country_id"],
			"iso":     isoCodes[city["country_id"].(uint8)],
			"name_en": "",
			"name_ru": "",
			"lat":     city["lat"],
			"lon":     city["lon"],
		}
		delete(city, "country_id")
	}

	if err == nil && city["region_seek"] != nil && city["region_seek"].(uint32) != 0 {
		if full {
			if !onlyCountry {
				region, err = f.unpack(city["region_seek"].(uint32), 1)
				if err != nil {
					return obj(), err
				}
				if region["country_seek"] != nil && region["country_seek"].(uint16) != 0 {
					country, err = f.unpack(uint32(region["country_seek"].(uint16)), 0)
				}
				delete(city, "region_seek")
				delete(region, "country_seek")
			}
		} else {
			delete(city, "region_seek")
		}
	}

	return map[string]interface{}{"country": country, "region": region, "city": city}, err
}

// GetCountry return string country iso-code, like `RU`, `UA` etc.
func (s *SxGEO) GetCountry(IP string) (string, error) {
	info, err := s.GetCity(IP)
	if err != nil {
		return "", err
	}
	return info["country"].(map[string]interface{})["iso"].(string), nil
}

// GetCountryID return integer country identifier
func (s *SxGEO) GetCountryID(IP string) (int, error) {
	info, err := s.GetCity(IP)
	if err != nil {
		return 0, err
	}
	return int(info["country"].(map[string]interface{})["id"].(uint8)), nil
}

// GetCityFull get full info by IP (with regions and countries data)
func (s *SxGEO) GetCityFull(IP string) (map[string]interface{}, error) {
	seek, err := s.finder.getLocationOffset(IP)
	if err != nil {
		return obj(), err
	}
	return s.finder.parseCity(seek, true)
}

// Info by IP
func (s *SxGEO) Info(IP string) (Result, error) {
	seek, err := s.finder.getLocationOffset(IP)
	if err != nil {
		return Result{}, err
	}
	return s.finder.parseToStruct(seek, true)
}

// City info by IP
func (s *SxGEO) City(IP string) (City, error) {
	seek, err := s.finder.getLocationOffset(IP)
	if err != nil {
		return City{}, err
	}
	ret, err2 := s.finder.parseToStruct(seek, false)
	return ret.City, err2
}

// Country info by IP
func (s *SxGEO) Country(IP string) (Country, error) {
	seek, err := s.finder.getLocationOffset(IP)
	if err != nil {
		return Country{}, err
	}
	ret, err2 := s.finder.parseToStruct(seek, true)
	return ret.Country, err2
}

// GetCity get short info by IP
func (s *SxGEO) GetCity(IP string) (map[string]interface{}, error) {
	seek, err := s.finder.getLocationOffset(IP)
	if err != nil {
		return obj(), err
	}
	return s.finder.parseCity(seek, false)
}

// New SxGEO object
func New(filename string) (*SxGEO, error) {
	dat, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, err
	} else if string(dat[:3]) != `SxG` && dat[3] != 22 && dat[8] != 2 {
		return nil, fmt.Errorf("Wrong database format")
	} else if dat[9] != 0 {
		return nil, fmt.Errorf("Only UTF-8 version is supported")
	}

	IDLen := uint32(dat[19])
	blockLen := 3 + IDLen
	DBItems := readUint32(dat, 15)
	BLen := uint32(dat[10])
	MLen := uint32(readUint16(dat, 11))
	packLen := uint32(readUint16(dat, 38))
	regnLen := readUint32(dat, 24)
	cityLen := readUint32(dat, 28)
	countryLen := readUint32(dat, 34)
	BStart := uint32(40 + packLen)
	MStart := BStart + BLen*4
	DBStart := MStart + MLen*4
	regnStart := DBStart + DBItems*blockLen
	cityStart := regnStart + regnLen
	cntrStart := cityStart + cityLen
	cntrEnd := cntrStart + countryLen
	pack := strings.Split(string(dat[40:40+packLen]), string(byte(0)))

	proto := &SxGEO{
		Version: float32(dat[3]) / 10,
		Updated: readUint32(dat, 4),
		finder: finder{
			Blocks:     uint32(readUint16(dat, 13)),
			DBItems:    DBItems,
			IDLen:      IDLen,
			BLen:       BLen,
			MLen:       MLen,
			CountryLen: countryLen,
			BlockLen:   blockLen,
			PackLen:    packLen,
			Pack:       pack,
			MaxRegion:  uint32(readUint16(dat, 20)),
			MaxCity:    uint32(readUint16(dat, 22)),
			MaxCountry: uint32(readUint16(dat, 32)),
			S: dbSlices{
				BIndex:    fullUint32(dat[BStart:MStart]),
				MIndex:    fullUint32(dat[MStart:DBStart]),
				DB:        dat[DBStart:regnStart],
				Regions:   dat[regnStart:cityStart],
				Cities:    dat[cityStart:cntrStart],
				Countries: dat[cntrStart:cntrEnd],
			},
		},
	}

	return proto, nil
}

func obj() (r map[string]interface{}) {
	r = map[string]interface{}{}
	return
}
