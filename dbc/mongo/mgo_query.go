package mongo

import (
	"fmt"
	"github.com/eaciit/crowd"
	"github.com/eaciit/dbox"
	"github.com/eaciit/errorlib"
	"github.com/eaciit/toolkit"
	"gopkg.in/mgo.v2"
)

const (
	modQuery = "Query"
)

type Query struct {
	dbox.Query

	session    *mgo.Session
	usePooling bool
}

func (q *Query) Session() *mgo.Session {
	q.usePooling = q.Config("pooling", false).(bool)
	if q.session == nil {
		if q.usePooling {
			q.session = q.Connection().(*Connection).session
		} else {
			q.session = q.Connection().(*Connection).session.Clone()
		}
	}
	return q.session
}

func (q *Query) Close() {
	if q.session != nil && q.usePooling == false {
		q.session.Close()
	}
}

func (q *Query) Prepare() error {
	return nil
}

func (q *Query) Cursor(in toolkit.M) (dbox.ICursor, error) {
	var e error
	/*
		if q.Parts == nil {
			return nil, errorlib.Error(packageName, modQuery,
				"Cursor", fmt.Sprintf("No Query Parts"))
		}
	*/

	aggregate := false
	dbname := q.Connection().Info().Database
	tablename := ""

	/*
		parts will return E - map{interface{}}interface{}
		where each interface{} returned is slice of interfaces --> []interface{}
	*/
	parts := crowd.From(q.Parts()).Group(func(x interface{}) interface{} {
		qp := x.(*dbox.QueryPart)
		return qp.PartType
	}, nil).Data

	fromParts, hasFrom := parts[dbox.QueryPartFrom]
	if hasFrom == false {
		return nil, errorlib.Error(packageName, "Query", "Cursor", "Invalid table name")
	}
	tablename = fromParts.([]interface{})[0].(*dbox.QueryPart).Value.(string)

	skip := 0
	if skipParts, hasSkip := parts[dbox.QueryPartSkip]; hasSkip {
		skip = skipParts.([]interface{})[0].(*dbox.QueryPart).
			Value.(int)
	}

	take := 0
	if takeParts, has := parts[dbox.QueryPartTake]; has {
		take = takeParts.([]interface{})[0].(*dbox.QueryPart).
			Value.(int)
	}

	aggrParts, hasAggr := parts[dbox.QueryPartAggr]
	if hasAggr {
		aggregate = true
		aggrExpression := toolkit.M{}
		aggrElements := func() []*dbox.QueryPart {
			var qps []*dbox.QueryPart
			for _, v := range aggrParts.([]interface{}) {
				qps = append(qps, v.(*dbox.QueryPart))
			}
			return qps
		}()
		for _, el := range aggrElements {
			aggr := el.Value.(dbox.AggrInfo)
			if aggr.Op == dbox.AggrSum {
				aggrExpression.Set(aggr.Alias, aggr.Field)
			}
		}
	}

	var fields toolkit.M
	selectParts, hasSelect := parts[dbox.QueryPartSelect]
	if hasSelect {
		fields = toolkit.M{}
		for _, sl := range selectParts.([]interface{}) {
			qp := sl.(*dbox.QueryPart)
			for _, fid := range qp.Value.([]string) {
				fields.Set(fid, 1)
			}
		}
	} else {
		_, hasUpdate := parts[dbox.QueryPartUpdate]
		_, hasInsert := parts[dbox.QueryPartInsert]
		_, hasDelete := parts[dbox.QueryPartDelete]
		_, hasSave := parts[dbox.QueryPartSave]

		if hasUpdate || hasInsert || hasDelete || hasSave {
			return nil, errorlib.Error(packageName, modQuery, "Cursor",
				"Valid operation for a cursor is select only")
		}
	}
	//fmt.Printf("Result: %s \n", toolkit.JsonString(fields))
	//fmt.Printf("Database:%s table:%s \n", dbname, tablename)
	var sort []string
	sortParts, hasSort := parts[dbox.QueryPartOrder]
	if hasSort {
		sort = []string{}
		for _, sl := range sortParts.([]interface{}) {
			qp := sl.(*dbox.QueryPart)
			for _, fid := range qp.Value.([]string) {
				sort = append(sort, fid)
			}
		}
	}

	//where := toolkit.M{}
	var where interface{}
	whereParts, hasWhere := parts[dbox.QueryPartWhere]
	if hasWhere {
		fb := q.Connection().Fb()
		for _, p := range whereParts.([]interface{}) {
			fs := p.(*dbox.QueryPart).Value.([]*dbox.Filter)
			for _, f := range fs {
				fb.AddFilter(f)
			}
		}
		where, e = fb.Build()
		if e != nil {
			return nil, errorlib.Error(packageName, modQuery, "Cursor",
				e.Error())
		} else {
			//fmt.Printf("Where: %s", toolkit.JsonString(where))
		}
		//where = iwhere.(toolkit.M)
	}

	session := q.Session()
	mgoColl := session.DB(dbname).C(tablename)
	cursor := dbox.NewCursor(new(Cursor))
	cursor.(*Cursor).session = session
	cursor.(*Cursor).isPoolingSession = q.usePooling

	if !aggregate {
		mgoCursor := mgoColl.Find(where)
		count, e := mgoCursor.Count()
		if e != nil {
			//fmt.Println("Error: " + e.Error())
			return nil, errorlib.Error(packageName,
				modQuery, "Cursor", e.Error())
		}
		if fields != nil {
			mgoCursor = mgoCursor.Select(fields)
		}
		if hasSort {
			mgoCursor = mgoCursor.Sort(sort...)
		}
		if skip > 0 {
			mgoCursor = mgoCursor.Skip(skip)
		}
		if take > 0 {
			mgoCursor = mgoCursor.Limit(take)
		}
		cursor.(*Cursor).ResultType = QueryResultCursor
		cursor.(*Cursor).mgoCursor = mgoCursor
		cursor.(*Cursor).count = count
		//cursor.(*Cursor).mgoIter = mgoCursor.Iter()
	} else {
		pipes := toolkit.M{}
		mgoPipe := session.DB(dbname).C(tablename).
			Pipe(pipes).AllowDiskUse()
		//iter := mgoPipe.Iter()

		cursor.(*Cursor).ResultType = QueryResultPipe
		cursor.(*Cursor).mgoPipe = mgoPipe
		//cursor.(*Cursor).mgoIter = iter
	}
	return cursor, nil
}

func (q *Query) Exec(parm toolkit.M) error {
	var e error
	if parm == nil {
		parm = toolkit.M{}
	}
	/*
		if q.Parts == nil {
			return errorlib.Error(packageName, modQuery,
				"Cursor", fmt.Sprintf("No Query Parts"))
		}
	*/

	dbname := q.Connection().Info().Database
	tablename := ""

	if parm == nil {
		parm = toolkit.M{}
	}
	data := parm.Get("data", nil)

	/*
		p arts will return E - map{interface{}}interface{}
		where each interface{} returned is slice of interfaces --> []interface{}
	*/
	parts := crowd.From(q.Parts()).Group(func(x interface{}) interface{} {
		qp := x.(*dbox.QueryPart)
		/*
			fmt.Printf("[%s] QP = %s \n",
				toolkit.Id(data),
				toolkit.JsonString(qp))
		*/
		return qp.PartType
	}, nil).Data

	fromParts, hasFrom := parts[dbox.QueryPartFrom]
	if !hasFrom {
		/*
			fmt.Printf("Data:\n%s\nParts:\n%s\nGrouped:\n%s\n",
				toolkit.JsonString(data),
				toolkit.JsonString(q.Parts()),
				toolkit.JsonString(parts))
		*/
		return errorlib.Error(packageName, "Query", modQuery, "Invalid table name")
	}
	tablename = fromParts.([]interface{})[0].(*dbox.QueryPart).Value.(string)

	var where interface{}
	commandType := ""
	multi := false

	_, hasDelete := parts[dbox.QueryPartDelete]
	_, hasInsert := parts[dbox.QueryPartInsert]
	_, hasUpdate := parts[dbox.QueryPartUpdate]
	_, hasSave := parts[dbox.QueryPartSave]

	if hasDelete {
		commandType = dbox.QueryPartDelete
	} else if hasInsert {
		commandType = dbox.QueryPartInsert
	} else if hasUpdate {
		commandType = dbox.QueryPartUpdate
	} else if hasSave {
		commandType = dbox.QueryPartSave
	}

	if data == nil {
		//---
		multi = true
	} else {
		if where == nil {
			id := toolkit.Id(data)
			if id != nil {
				where = (toolkit.M{}).Set("_id", id)
			}
		} else {
			multi = true
		}
	}

	session := q.Session()

	multiExec := q.Config("multiexec", false).(bool)
	if !multiExec && !q.usePooling && session != nil {
		defer session.Close()
	}
	mgoColl := session.DB(dbname).C(tablename)
	if commandType == dbox.QueryPartInsert {
		e = mgoColl.Insert(data)
	} else if commandType == dbox.QueryPartUpdate {
		if multi {
			_, e = mgoColl.UpdateAll(where, data)
		} else {
			e = mgoColl.Update(where, data)
			if e != nil {
				e = fmt.Errorf("%s [%v]", e.Error(), where)
			}
		}
	} else if commandType == dbox.QueryPartDelete {
		if multi {
			_, e = mgoColl.RemoveAll(where)
		} else {
			e = mgoColl.Remove(where)
			if e != nil {
				e = fmt.Errorf("%s [%v]", e.Error(), where)
			}
		}
	} else if commandType == dbox.QueryPartSave {
		_, e = mgoColl.Upsert(where, data)
	}
	if e != nil {
		return errorlib.Error(packageName, modQuery+".Exec", commandType, e.Error())
	}
	return nil
}
