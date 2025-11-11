package models

import "time"

type User struct {
    ID                 int64     `json:"id"`
    Name               string    `json:"name"`
    BusinessName       string    `json:"business_name"`
    Email              string    `json:"email"`
    PasswordHash       string    `json:"-"`
    Phone              string    `json:"phone"`
    IsWhatsAppVerified bool      `json:"is_whatsapp_verified"`
    IndustryType       *string   `json:"industry_type"`
    SubIndustry        *string   `json:"sub_industry"`
    CoreProcesses      []string  `json:"core_processes"`
    MonthlyRevenue     *float64  `json:"monthly_revenue"`
    Employees          *int      `json:"employees"`
    GoalAmount         *float64  `json:"goal_amount"`
    GoalYears          *int      `json:"goal_years"`
    CreatedAt          time.Time `json:"created_at"`
}
