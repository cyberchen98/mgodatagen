package datagen

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/feliixx/mgodatagen/datagen/generators"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/bsontype"
)

type stdoutWriter struct {
	*basicGenerator
	prettyPrint bool
}

func newStdoutWriter(options *Options) (writer, error) {
	return &stdoutWriter{
		basicGenerator: &basicGenerator{
			batchSize:  1000,
			mapRef:     make(map[int][][]byte),
			mapRefType: make(map[int]bsontype.Type),
		},
		prettyPrint: options.PrettyPrint,
	}, nil
}

func (w *stdoutWriter) write(collections []Collection, seed uint64) (err error) {

	for i := 0; i < len(collections); i++ {

		ci := generators.NewCollInfo(collections[i].Count, []int{5, 0, 6}, seed, w.mapRef, w.mapRefType)

		// as the document is not inserted in mongodb, the "_id" won't be autogenerated
		// if not present, so add an objectId generator if user hasn't specified one
		_, hasId := collections[i].Content["_id"]
		if !hasId {
			collections[i].Content["_id"] = generators.Config{
				Type: generators.TypeObjectID,
			}
		}
		collections[i].docGenerator, err = ci.NewDocumentGenerator(collections[i].Content)
		if err != nil {
			return fmt.Errorf("fail to create DocumentGenerator for collection '%s'\n%v", collections[i].Name, err)
		}
	}

	for i := 0; i < len(collections); i++ {
		w.generate(&collections[i])
	}
	return nil
}

func (w *stdoutWriter) generate(coll *Collection) {

	tasks := make(chan *rawChunk, 5)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go w.writeToStdout(ctx, cancel, &wg, coll, tasks)

	w.generateDocument(ctx, tasks, coll.Count, coll.docGenerator)

	wg.Wait()
}

func (w *stdoutWriter) writeToStdout(ctx context.Context, cancel context.CancelFunc, wg *sync.WaitGroup, coll *Collection, tasks <-chan *rawChunk) {

	defer wg.Done()

	buffer := bytes.NewBuffer(make([]byte, 0, 64000))
	fmt.Fprintf(buffer, `{
  "%s.%s": [
`, coll.DB, coll.Name)

	prefix := "    "

	for t := range tasks {
		// if an error occurs in one of the goroutine, 'return' is called which trigger
		// wg.Done() ==> the goroutine stops
		select {
		case <-ctx.Done():
			return
		default:
		}

		for _, doc := range t.documents[:t.nbToInsert] {

			raw := bson.Raw(doc)

			buffer.Write([]byte(prefix))
			if w.prettyPrint {
				bson.IndentExtJSON(buffer, []byte(raw.String()), prefix, "  ")
			} else {
				buffer.Write([]byte(raw.String()))
			}
			buffer.WriteByte(',')
			buffer.WriteByte('\n')

			if buffer.Len() > 64000 {
				os.Stdout.Write(buffer.Bytes())
				buffer.Reset()
			}
		}
		pool.Put(t)
	}

	// remove the last trailing comma
	buffer.Truncate(buffer.Len() - 2)
	fmt.Fprintln(buffer, `
  ]
}`)

	if buffer.Len() > 0 {
		os.Stdout.Write(buffer.Bytes())
	}
}