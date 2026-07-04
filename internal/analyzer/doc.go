// Package analyzer scores prompts before routing. It defines the
// PromptAnalyzer interface and its implementations:
//
//   - HeuristicAnalyzer: deterministic, explainable signal scoring (default)
//   - LLMAnalyzer: optional classification via a small local Ollama model,
//     falling back to the heuristic on any failure
//
// Analyzers produce a complexity score in [0,1] and a category
// (chat, code, math, analysis, general) that drive model selection.
package analyzer
