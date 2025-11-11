package models

type RegisterRequest struct {
    Name         string `json:"name"`
    Email        string `json:"email"`
    Password     string `json:"password"`
    Confirm      string `json:"confirm_password"`
    Phone        string `json:"whatsapp_number"`
}

type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type OTPRequest struct {
	Phone string `json:"phone_number"`
}

type OTPVerifyRequest struct {
	Phone string `json:"phone_number"`
	OTP   string `json:"otp"`
}

type CompanySetupRequest struct {
    BusinessName   string   `json:"business_name"`
    MonthlyRevenue float64  `json:"monthly_revenue"`
    Employees      int      `json:"employees"`
    GoalAmount     float64  `json:"goal_amount"`
    GoalYears      int      `json:"goal_years"`
    IndustryType   string   `json:"industry_type"`
    SubIndustry    string   `json:"sub_industry"`
    CoreProcesses  []string `json:"core_processes"`
}
