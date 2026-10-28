package models

// Лун без перевода номера в int
func ValidOrder(number string) bool {
	if number == "" {
		return false
	}
	sum := 0
	double := false

	for i := len(number) - 1; i >= 0; i-- {
		if number[i] < '0' || number[i] > '9' {
			return false
		}

		digit := int(number[i] - '0')
		if double {
			digit *= 2
			if digit > 9 {
				digit -= 9
			}
		}
		sum = (sum + digit) % 10
		double = !double
	}

	return sum == 0
}
