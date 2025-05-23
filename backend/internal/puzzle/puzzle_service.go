package puzzle

import (
	"fmt"
	"math"
	"math/rand"
	"regexp"
	"strings"
	"time"

	"github.com/hectoclash/internal/models"
	"github.com/hectoclash/internal/repository"
)

// Service provides puzzle functionality
type Service struct {
	puzzleRepo *repository.PuzzleRepository
	cache      *PuzzleCache
}

// NewService creates a new puzzle service
func NewService(puzzleRepo *repository.PuzzleRepository) *Service {
	// Create a cache with 1000 puzzles max and 24-hour expiration
	cache := NewPuzzleCache(1000, 24*time.Hour)

	return &Service{
		puzzleRepo: puzzleRepo,
		cache:      cache,
	}
}

// GeneratePuzzle generates a new Hectoc puzzle
func (s *Service) GeneratePuzzle() (*models.Puzzle, error) {
	// Generate a random sequence
	sequence := s.generateRandomSequence()

	// Check if the puzzle already exists
	existingPuzzle, err := s.puzzleRepo.FindBySequence(sequence)
	if err == nil {
		// Puzzle already exists, return it
		return existingPuzzle, nil
	}

	// Generate solutions for the puzzle
	solutions, err := s.generateSolutions(sequence)
	if err != nil {
		return nil, err
	}

	if len(solutions) == 0 {
		// No solutions found, try again with a new sequence
		return s.GeneratePuzzle()
	}

	// Find the optimal solution
	optimalSolution := s.findOptimalSolution(solutions)

	// Calculate complexity score
	complexityScore := s.calculateComplexityScore(sequence, solutions)

	// Determine difficulty level based on complexity
	difficulty := s.determineDifficulty(complexityScore, len(solutions))

	// Create explanation for the optimal solution
	explanation := s.createExplanation(optimalSolution)

	// Create the puzzle
	puzzle := &models.Puzzle{
		Sequence:        sequence,
		Difficulty:      difficulty,
		ComplexityScore: complexityScore,
		SolutionCount:   len(solutions),
		OptimalSolution: optimalSolution,
		Explanation:     explanation,
		MinELO:          s.calculateMinELO(difficulty),
		MaxELO:          s.calculateMaxELO(difficulty),
	}

	// Save the puzzle
	err = s.puzzleRepo.Create(puzzle)
	if err != nil {
		return nil, err
	}

	// Save the solutions
	for _, solution := range solutions {
		solutionObj := &models.PuzzleSolution{
			PuzzleID:   puzzle.ID,
			Expression: solution,
			Complexity: s.calculateSolutionComplexity(solution),
			IsOptimal:  solution == optimalSolution,
		}
		err = s.puzzleRepo.CreateSolution(solutionObj)
		if err != nil {
			return nil, err
		}
	}

	return puzzle, nil
}

// GetPuzzle gets a puzzle by ID
func (s *Service) GetPuzzle(id string) (*models.Puzzle, error) {
	// Check cache first
	puzzle := s.cache.Get(id)
	if puzzle != nil {
		return puzzle, nil
	}

	// Get from database
	puzzle, err := s.puzzleRepo.FindByID(id)
	if err != nil {
		return nil, err
	}

	// Add to cache
	s.cache.Set(puzzle)

	return puzzle, nil
}

// GetPuzzleForUser gets a puzzle suitable for a user's ELO rating
func (s *Service) GetPuzzleForUser(userELO int) (*models.Puzzle, error) {
	// Try to get a puzzle from cache first
	puzzle := s.cache.GetByELO(userELO)
	if puzzle != nil {
		return puzzle, nil
	}

	// Try to get a random puzzle within the user's ELO range from database
	puzzle, err := s.puzzleRepo.GetRandomPuzzleByELORange(userELO)
	if err == nil {
		// Add to cache
		s.cache.Set(puzzle)
		return puzzle, nil
	}

	// If no puzzle found, generate a new one
	puzzle, err = s.GeneratePuzzle()
	if err != nil {
		return nil, err
	}

	// Add to cache
	s.cache.Set(puzzle)
	return puzzle, nil
}

// ValidateSolution validates a solution for a puzzle
func (s *Service) ValidateSolution(puzzleID, solution string) (bool, error) {
	// Get the puzzle (using cache if available)
	puzzle, err := s.GetPuzzle(puzzleID)
	if err != nil {
		return false, err
	}

	// Clean the solution
	solution = s.cleanSolution(solution)

	// Check if the solution uses all digits in the correct order
	if !s.usesAllDigitsInOrder(puzzle.Sequence, solution) {
		return false, nil
	}

	// Evaluate the solution
	result, err := s.evaluateExpression(solution)
	if err != nil {
		return false, err
	}

	// Check if the result equals 100
	isCorrect := result == 100

	// If the solution is correct, update puzzle stats in the background
	if isCorrect {
		go func() {
			// Calculate solve time (this is just an estimate since we don't track when the user started)
			solveTime := 30.0 // Default to 30 seconds if we don't have actual timing

			// Update puzzle stats
			_ = s.puzzleRepo.UpdatePuzzleStats(puzzleID, solveTime, true)
		}()
	}

	return isCorrect, nil
}

// GetPuzzlesByDifficulty gets puzzles by difficulty level
func (s *Service) GetPuzzlesByDifficulty(difficulty models.DifficultyLevel, limit, offset int) ([]models.Puzzle, error) {
	return s.puzzleRepo.GetPuzzlesByDifficulty(difficulty, limit, offset)
}

// GetPuzzlesByELORange gets puzzles suitable for a specific ELO range
func (s *Service) GetPuzzlesByELORange(elo, limit, offset int) ([]models.Puzzle, error) {
	return s.puzzleRepo.GetPuzzlesByELORange(elo, limit, offset)
}

// PreGeneratePuzzles pre-generates a specified number of puzzles for each difficulty level
func (s *Service) PreGeneratePuzzles(countPerDifficulty int) error {
	// Create a generator
	generator := NewPuzzleGenerator()

	// Generate puzzles for each difficulty level
	for difficulty := models.DifficultyEasy; difficulty <= models.DifficultyChampion; difficulty++ {
		// Check how many puzzles already exist for this difficulty
		count, err := s.puzzleRepo.CountPuzzlesByDifficulty(difficulty)
		if err != nil {
			return err
		}

		// Calculate how many more puzzles we need
		needed := countPerDifficulty - int(count)
		if needed <= 0 {
			continue // We have enough puzzles for this difficulty
		}

		// Generate more puzzles if needed
		for i := 0; i < needed; i++ {
			// Try to generate a puzzle with the target difficulty
			sequence, solutions, err := generator.GeneratePuzzleWithDifficulty(int(difficulty))
			if err != nil {
				// If we can't generate a puzzle with the exact difficulty, just generate a random one
				puzzle, err := s.GeneratePuzzle()
				if err != nil {
					return err
				}

				// Add to cache
				s.cache.Set(puzzle)
				continue
			}

			// Find the optimal solution
			optimalSolution := generator.FindOptimalSolution(solutions)

			// Calculate complexity score
			complexityScore := s.calculateComplexityScore(sequence, solutions)

			// Create explanation for the optimal solution
			explanation := generator.CreateExplanation(optimalSolution)

			// Create the puzzle
			puzzle := &models.Puzzle{
				Sequence:        sequence,
				Difficulty:      difficulty,
				ComplexityScore: complexityScore,
				SolutionCount:   len(solutions),
				OptimalSolution: optimalSolution,
				Explanation:     explanation,
				MinELO:          s.calculateMinELO(difficulty),
				MaxELO:          s.calculateMaxELO(difficulty),
			}

			// Save the puzzle
			err = s.puzzleRepo.Create(puzzle)
			if err != nil {
				return err
			}

			// Save the solutions
			for _, solution := range solutions {
				solutionObj := &models.PuzzleSolution{
					PuzzleID:   puzzle.ID,
					Expression: solution,
					Complexity: s.calculateSolutionComplexity(solution),
					IsOptimal:  solution == optimalSolution,
				}
				err = s.puzzleRepo.CreateSolution(solutionObj)
				if err != nil {
					return err
				}
			}

			// Add to cache
			s.cache.Set(puzzle)
		}
	}
	return nil
}

// UpdatePuzzleStats updates the statistics for a puzzle after a game
func (s *Service) UpdatePuzzleStats(puzzleID string, solveTime float64, isCorrect bool) error {
	return s.puzzleRepo.UpdatePuzzleStats(puzzleID, solveTime, isCorrect)
}

// Helper function to generate a random 6-digit sequence
func (s *Service) generateRandomSequence() string {
	// Create a local random generator with a random source
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	// Generate 6 random digits between 1 and 9
	digits := make([]byte, 6)
	for i := 0; i < 6; i++ {
		digits[i] = byte(rng.Intn(9) + 1 + '0')
	}

	return string(digits)
}

// Helper function to generate all possible solutions for a puzzle
func (s *Service) generateSolutions(sequence string) ([]string, error) {
	// Create a puzzle generator
	generator := NewPuzzleGenerator()

	// Generate all possible solutions
	allSolutions := generator.GenerateSolutions(sequence)

	// Filter solutions that equal 100
	validSolutions := []string{}
	for _, solution := range allSolutions {
		result, err := s.evaluateExpression(solution)
		if err == nil && result == 100 {
			validSolutions = append(validSolutions, solution)
		}
	}

	// If we didn't find any solutions, try with some common patterns
	if len(validSolutions) == 0 {
		digits := []rune(sequence)

		// Try some common patterns that often yield solutions
		commonPatterns := []string{
			fmt.Sprintf("%c+%c*%c*%c*%c+%c", digits[0], digits[1], digits[2], digits[3], digits[4], digits[5]),
			fmt.Sprintf("(%c+%c)*(%c+%c+%c+%c)", digits[0], digits[1], digits[2], digits[3], digits[4], digits[5]),
			fmt.Sprintf("%c*(%c+%c*%c)+%c*%c", digits[0], digits[1], digits[2], digits[3], digits[4], digits[5]),
			fmt.Sprintf("%c*%c+%c*%c+%c*%c", digits[0], digits[1], digits[2], digits[3], digits[4], digits[5]),
			fmt.Sprintf("(%c+%c+%c)*(%c+%c+%c)", digits[0], digits[1], digits[2], digits[3], digits[4], digits[5]),
			fmt.Sprintf("%c*%c*%c+%c*%c+%c", digits[0], digits[1], digits[2], digits[3], digits[4], digits[5]),
		}

		// Check each pattern
		for _, pattern := range commonPatterns {
			result, err := s.evaluateExpression(pattern)
			if err == nil && result == 100 {
				validSolutions = append(validSolutions, pattern)
			}
		}
	}

	return validSolutions, nil
}

// Helper function to find the optimal solution among all solutions
func (s *Service) findOptimalSolution(solutions []string) string {
	if len(solutions) == 0 {
		return ""
	}

	// Find the solution with the lowest complexity
	optimalSolution := solutions[0]
	minComplexity := s.calculateSolutionComplexity(optimalSolution)

	for _, solution := range solutions[1:] {
		complexity := s.calculateSolutionComplexity(solution)
		if complexity < minComplexity {
			minComplexity = complexity
			optimalSolution = solution
		}
	}

	return optimalSolution
}

// Helper function to calculate the complexity of a solution
func (s *Service) calculateSolutionComplexity(solution string) float64 {
	// Count the number of operators with different weights
	addSubCount := strings.Count(solution, "+") + strings.Count(solution, "-")
	mulDivCount := strings.Count(solution, "*") + strings.Count(solution, "/")
	parenthesesCount := strings.Count(solution, "(") + strings.Count(solution, ")")

	// Calculate base complexity
	// Addition/subtraction: 1 point each
	// Multiplication/division: 1.5 points each
	// Parentheses: 0.5 points each
	baseComplexity := float64(addSubCount) + float64(mulDivCount)*1.5 + float64(parenthesesCount)*0.5

	// Calculate nesting depth (more nesting = more complex)
	maxDepth := 0
	currentDepth := 0
	for _, char := range solution {
		if char == '(' {
			currentDepth++
			if currentDepth > maxDepth {
				maxDepth = currentDepth
			}
		} else if char == ')' {
			currentDepth--
		}
	}

	// Add depth factor (each level of nesting adds 0.5 to complexity)
	depthFactor := float64(maxDepth) * 0.5

	// Calculate length factor (longer expressions are more complex)
	lengthFactor := math.Log10(float64(len(solution))) * 0.5

	// Combine all factors
	totalComplexity := baseComplexity + depthFactor + lengthFactor

	return totalComplexity
}

// Helper function to calculate the overall complexity score of a puzzle
func (s *Service) calculateComplexityScore(_ string, solutions []string) float64 {
	if len(solutions) == 0 {
		return 0
	}

	// Calculate the average complexity of all solutions
	totalComplexity := 0.0
	for _, solution := range solutions {
		totalComplexity += s.calculateSolutionComplexity(solution)
	}
	avgComplexity := totalComplexity / float64(len(solutions))

	// Adjust complexity based on the number of solutions (fewer solutions = more complex)
	solutionFactor := 1.0 + (1.0 / float64(len(solutions)))

	return avgComplexity * solutionFactor
}

// Helper function to determine the difficulty level based on complexity
func (s *Service) determineDifficulty(complexityScore float64, solutionCount int) models.DifficultyLevel {
	// Calculate a combined difficulty score (0-10 scale)
	// Complexity contributes 70%, solution count contributes 30%
	complexityFactor := math.Min(complexityScore/10.0, 1.0) * 7.0 // 0-7 scale

	// Fewer solutions = higher difficulty
	var solutionFactor float64
	switch {
	case solutionCount <= 1:
		solutionFactor = 3.0 // Maximum difficulty
	case solutionCount <= 3:
		solutionFactor = 2.5
	case solutionCount <= 5:
		solutionFactor = 2.0
	case solutionCount <= 10:
		solutionFactor = 1.5
	case solutionCount <= 20:
		solutionFactor = 1.0
	default:
		solutionFactor = 0.5 // Minimum difficulty
	}

	// Calculate combined score (0-10 scale)
	combinedScore := complexityFactor + solutionFactor

	// Map to difficulty levels
	switch {
	case combinedScore < 2.0:
		return models.DifficultyEasy
	case combinedScore < 4.0:
		return models.DifficultyMedium
	case combinedScore < 6.0:
		return models.DifficultyHard
	case combinedScore < 8.0:
		return models.DifficultyExpert
	default:
		return models.DifficultyChampion
	}
}

// Helper function to create an explanation for a solution
func (s *Service) createExplanation(solution string) string {
	// Create a detailed explanation of the solution
	explanation := "Step-by-step solution:\n"

	// Parse the solution into tokens for explanation
	evaluator := NewExpressionEvaluator()
	tokens, err := evaluator.tokenize(solution)
	if err != nil {
		// If we can't parse it, just return the basic explanation
		return fmt.Sprintf("Solution: %s = 100", solution)
	}

	// Try to break down the solution into steps
	explanation += s.explainSolution(solution, tokens)

	return explanation
}

// Helper function to explain a solution step by step
func (s *Service) explainSolution(solution string, _ []Token) string {
	// Initialize explanation
	explanation := ""

	// Find parenthesized expressions and explain them first
	groups := s.findParenthesizedGroups(solution)
	if len(groups) > 0 {
		explanation += "Breaking down the expression:\n"

		// Explain each parenthesized group
		for i, group := range groups {
			// Evaluate the group
			result, err := s.evaluateExpression(group)
			if err == nil {
				explanation += fmt.Sprintf("  Step %d: Calculate (%s) = %.0f\n", i+1, group, result)
			}
		}
	}

	// Explain the final calculation
	explanation += fmt.Sprintf("  Final result: %s = 100\n", solution)

	// Add a note about the operations used
	addCount := strings.Count(solution, "+")
	subCount := strings.Count(solution, "-")
	mulCount := strings.Count(solution, "*")
	divCount := strings.Count(solution, "/")
	parenCount := strings.Count(solution, "(") // Count opening parentheses

	explanation += "\nThis solution uses:"
	if addCount > 0 {
		explanation += fmt.Sprintf("\n- Addition (%d times)", addCount)
	}
	if subCount > 0 {
		explanation += fmt.Sprintf("\n- Subtraction (%d times)", subCount)
	}
	if mulCount > 0 {
		explanation += fmt.Sprintf("\n- Multiplication (%d times)", mulCount)
	}
	if divCount > 0 {
		explanation += fmt.Sprintf("\n- Division (%d times)", divCount)
	}
	if parenCount > 0 {
		explanation += fmt.Sprintf("\n- Parentheses (%d groups)", parenCount)
	}

	return explanation
}

// Helper function to find parenthesized groups in an expression
func (s *Service) findParenthesizedGroups(expression string) []string {
	groups := []string{}
	stack := []int{}

	for i, char := range expression {
		if char == '(' {
			stack = append(stack, i)
		} else if char == ')' && len(stack) > 0 {
			start := stack[len(stack)-1]
			stack = stack[:len(stack)-1]

			// Extract the group without the outer parentheses
			group := expression[start+1:i]
			groups = append(groups, group)
		}
	}

	return groups
}

// Helper function to calculate the minimum ELO rating for a difficulty level
func (s *Service) calculateMinELO(difficulty models.DifficultyLevel) int {
	switch difficulty {
	case models.DifficultyEasy:
		return 0
	case models.DifficultyMedium:
		return 1000
	case models.DifficultyHard:
		return 1500
	case models.DifficultyExpert:
		return 2000
	case models.DifficultyChampion:
		return 2500
	default:
		return 0
	}
}

// Helper function to calculate the maximum ELO rating for a difficulty level
func (s *Service) calculateMaxELO(difficulty models.DifficultyLevel) int {
	switch difficulty {
	case models.DifficultyEasy:
		return 1200
	case models.DifficultyMedium:
		return 1700
	case models.DifficultyHard:
		return 2200
	case models.DifficultyExpert:
		return 2700
	case models.DifficultyChampion:
		return 3000
	default:
		return 3000
	}
}

// Helper function to clean a solution string
func (s *Service) cleanSolution(solution string) string {
	// Remove all whitespace
	solution = strings.ReplaceAll(solution, " ", "")

	// Replace × with * and ÷ with /
	solution = strings.ReplaceAll(solution, "×", "*")
	solution = strings.ReplaceAll(solution, "÷", "/")

	return solution
}

// Helper function to check if a solution uses all digits in the correct order
func (s *Service) usesAllDigitsInOrder(sequence, solution string) bool {
	// Remove all non-digit characters from the solution
	re := regexp.MustCompile("[^0-9]")
	digits := re.ReplaceAllString(solution, "")

	// Check if the digits match the sequence
	return digits == sequence
}

// Helper function to evaluate a mathematical expression
func (s *Service) evaluateExpression(expression string) (float64, error) {
	// Use our expression evaluator to properly evaluate the expression
	evaluator := NewExpressionEvaluator()
	return evaluator.Evaluate(expression)
}

// ELO rating calculation functions

// CalculateELOChange calculates the change in ELO rating after a game
func (s *Service) CalculateELOChange(playerRating, puzzleDifficulty int, isCorrect bool, solveTime float64) int {
	// Constants for ELO calculation
	k := 32 // K-factor, determines the maximum possible adjustment

	// Calculate expected score based on ratings
	expectedScore := 1 / (1 + math.Pow(10, float64(puzzleDifficulty-playerRating)/400))

	// Actual score (1 for win, 0 for loss)
	actualScore := 0.0
	if isCorrect {
		actualScore = 1.0

		// Adjust score based on solve time (faster solve = higher score)
		// This is a simplified version. You can adjust the formula based on your requirements.
		timeBonus := math.Max(0, 1 - (solveTime / 300)) // 300 seconds (5 minutes) as reference
		actualScore += timeBonus * 0.5 // Maximum 50% bonus for very fast solves
	}

	// Calculate ELO change
	eloChange := int(math.Round(float64(k) * (actualScore - expectedScore)))

	// Limit the maximum change
	if eloChange > 50 {
		eloChange = 50
	} else if eloChange < -50 {
		eloChange = -50
	}

	return eloChange
}

// GetPuzzleDifficultyRating converts a difficulty level to an ELO-equivalent rating
func (s *Service) GetPuzzleDifficultyRating(difficulty models.DifficultyLevel) int {
	switch difficulty {
	case models.DifficultyEasy:
		return 800
	case models.DifficultyMedium:
		return 1300
	case models.DifficultyHard:
		return 1800
	case models.DifficultyExpert:
		return 2300
	case models.DifficultyChampion:
		return 2800
	default:
		return 1000
	}
}

// GetUserELO gets a user's ELO rating from the repository
func (s *Service) GetUserELO(userID string) (int, error) {
	// This would typically involve a call to the user repository
	// For now, we'll use a placeholder implementation

	// In a real implementation, you would inject the user repository and call it
	// For example: return s.userRepo.GetUserRating(userID)

	// For now, return a default rating
	return 1000, nil
}
