// Command eval-grader grades agent answers against replay cases
// deterministically (PLAN.md E.2): every gold evidence item must be cited
// and must reproduce when re-run; the verdict must match.
//
//	eval-grader -case case.json -answer answer.json -fixture fixture.json
//	eval-grader -cases dir/ -answers dir/ -forward [-record out.json]
//
// With -forward, re-runs go to the Forward instance in the environment
// (FORWARD_API_*), through the same certified library and client the MCP
// server uses. With -fixture, re-runs are replayed from a recording.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/forward-mcp/internal/adapters/certifiedfs"
	"github.com/forward-mcp/internal/adapters/evalrerun"
	"github.com/forward-mcp/internal/adapters/forwardclient"
	"github.com/forward-mcp/internal/config"
	"github.com/forward-mcp/internal/domain/entities"
	"github.com/forward-mcp/internal/domain/services/eval"
	"github.com/forward-mcp/internal/forward"
	"github.com/forward-mcp/internal/logger"
	"github.com/forward-mcp/internal/ports"
	"github.com/forward-mcp/internal/service"
	"github.com/forward-mcp/internal/usecases"
)

func main() {
	var (
		caseFile   = flag.String("case", "", "one case JSON file")
		answerFile = flag.String("answer", "", "one answer JSON file")
		casesDir   = flag.String("cases", "", "directory of case-*.json files")
		answersDir = flag.String("answers", "", "directory of answer-<case id>.json files")
		fixture    = flag.String("fixture", "", "replay re-runs from this recording")
		useForward = flag.Bool("forward", false, "re-run against the Forward instance in the environment")
		record     = flag.String("record", "", "with -forward: save re-runs to this fixture file")
		out        = flag.String("out", "", "write per-case reports (JSONL) here; default stdout")
		verbose    = flag.Bool("v", false, "print each report")
	)
	flag.Parse()

	cases, answers, err := load(*caseFile, *answerFile, *casesDir, *answersDir)
	if err != nil {
		fatal(err)
	}

	var rerun ports.EvidenceRerun
	var recorder *evalrerun.Recorder
	switch {
	case *fixture != "":
		rerun, err = evalrerun.LoadFixture(*fixture)
		if err != nil {
			fatal(err)
		}
	case *useForward:
		rerun, err = forwardBackend()
		if err != nil {
			fatal(err)
		}
		if *record != "" {
			recorder = evalrerun.NewRecorder(rerun)
			rerun = recorder
		}
	default:
		fatal(fmt.Errorf("choose -fixture <file> or -forward"))
	}

	g := eval.New(rerun)
	var reports []entities.GradeReport
	var w *os.File = os.Stdout
	if *out != "" {
		w, err = os.Create(*out)
		if err != nil {
			fatal(err)
		}
		defer w.Close()
	}
	enc := json.NewEncoder(w)
	for _, c := range cases {
		a, ok := answers[c.ID]
		if !ok {
			fmt.Fprintf(os.Stderr, "warning: no answer for case %s; grading as empty\n", c.ID)
		}
		r := g.Grade(context.Background(), c, a)
		reports = append(reports, r)
		if *verbose || *out != "" {
			if err := enc.Encode(r); err != nil {
				fatal(err)
			}
		}
	}
	if recorder != nil {
		if err := recorder.Save(*record); err != nil {
			fatal(err)
		}
		fmt.Fprintf(os.Stderr, "recorded re-runs to %s\n", *record)
	}

	s := eval.Summarize(reports)
	b, _ := json.MarshalIndent(s, "", "  ")
	fmt.Fprintln(os.Stderr, string(b))
	if s.Accurate < s.Cases {
		os.Exit(1)
	}
}

func load(caseFile, answerFile, casesDir, answersDir string) ([]entities.EvalCase, map[string]entities.AgentAnswer, error) {
	var cases []entities.EvalCase
	answers := map[string]entities.AgentAnswer{}
	switch {
	case caseFile != "":
		var c entities.EvalCase
		if err := readJSON(caseFile, &c); err != nil {
			return nil, nil, err
		}
		cases = append(cases, c)
		if answerFile != "" {
			var a entities.AgentAnswer
			if err := readJSON(answerFile, &a); err != nil {
				return nil, nil, err
			}
			answers[c.ID] = a
		}
	case casesDir != "":
		files, _ := filepath.Glob(filepath.Join(casesDir, "case-*.json"))
		sort.Strings(files)
		for _, f := range files {
			var c entities.EvalCase
			if err := readJSON(f, &c); err != nil {
				return nil, nil, err
			}
			cases = append(cases, c)
			if answersDir != "" {
				var a entities.AgentAnswer
				af := filepath.Join(answersDir, "answer-"+c.ID+".json")
				if err := readJSON(af, &a); err == nil {
					answers[c.ID] = a
				}
			}
		}
	default:
		return nil, nil, fmt.Errorf("give -case or -cases")
	}
	if len(cases) == 0 {
		return nil, nil, fmt.Errorf("no cases found")
	}
	return cases, answers, nil
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// forwardBackend wires the live re-run backend from the environment with
// the same components the MCP server uses.
func forwardBackend() (ports.EvidenceRerun, error) {
	cfg := config.LoadConfig()
	if cfg.Forward.APIBaseURL == "" {
		return nil, fmt.Errorf("-forward needs FORWARD_API_BASE_URL")
	}
	if err := cfg.Forward.ValidateAuth(); err != nil {
		return nil, fmt.Errorf("-forward: %w", err)
	}
	log := logger.New()
	client := forward.NewClient(&cfg.Forward)
	nqe := forwardclient.NewNQERunner(client)
	var certified *usecases.CertifiedQueries
	if dir := cfg.Forward.CertifiedLibraryPath; dir != "" {
		uc, err := usecases.NewCertifiedQueries(context.Background(), certifiedfs.New(os.DirFS(dir)), service.NewKeywordEmbeddingService(), nqe)
		if err != nil {
			log.Warn("certified library unavailable to the grader: %v", err)
		} else {
			certified = uc
		}
	}
	return evalrerun.NewForward(certified, nqe, client), nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "eval-grader:", strings.TrimSpace(err.Error()))
	os.Exit(2)
}
