package tasks

import (
	"time"

	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
)

func CreateTask(taskId string, clients []string, command string) error {
	db := dbcore.GetDBInstance()
	// Create a new task in the database with clients as JSON array
	task := models.Task{
		TaskId:  taskId,
		Clients: models.StringArray(clients),
		Command: command,
	}
	if err := db.Create(&task).Error; err != nil {
		return err
	}
	var taskResults []models.TaskResult
	for _, client := range clients {
		taskResults = append(taskResults, models.TaskResult{
			TaskId:     taskId,
			Client:     client,
			Result:     "",
			ExitCode:   nil,
			FinishedAt: nil,
			CreatedAt:  time.Now().UTC(),
		})
	}
	if len(taskResults) > 0 {
		return db.Create(&taskResults).Error
	}
	return nil
}
func GetTaskByTaskId(taskId string) (*models.Task, error) {
	var task models.Task
	if err := dbcore.GetDBInstance().Where("task_id = ?", taskId).First(&task).Error; err != nil {
		return nil, err
	}
	return &task, nil
}
func GetTasksByClientId(clientId string) ([]models.Task, error) {
	var tasks []models.Task
	if err := dbcore.GetDBInstance().Where("clients LIKE ?", "%"+clientId+"%").Find(&tasks).Error; err != nil {
		return nil, err
	}
	return tasks, nil
}

func GetSpecificTaskResult(taskId, clientId string) (*models.TaskResult, error) {
	var result models.TaskResult
	if err := dbcore.GetDBInstance().Where("task_id = ? AND client = ?", taskId, clientId).First(&result).Error; err != nil {
		return nil, err
	}
	return &result, nil
}

func GetAllTasks() ([]models.Task, error) {
	var tasks []models.Task
	if err := dbcore.GetDBInstance().Find(&tasks).Error; err != nil {
		return nil, err
	}
	return tasks, nil
}

func GetTaskResultsByTaskId(taskId string) ([]models.TaskResult, error) {
	var results []models.TaskResult
	if err := dbcore.GetDBInstance().Where("task_id = ?", taskId).Find(&results).Error; err != nil {
		return nil, err
	}
	return results, nil
}
func SaveTaskResult(taskId, clientId, result string, exitCode int, timestamp time.Time) error {
	return dbcore.GetDBInstance().
		Model(&models.TaskResult{}).
		Where("task_id = ? AND client = ?", taskId, clientId).
		Updates(map[string]interface{}{
			"result":      result,
			"exit_code":   exitCode,
			"finished_at": timestamp.UTC(),
		}).Error
}

func ClearTaskResultsByTimeBefore(before time.Time) error {
	return dbcore.GetDBInstance().Where("created_at < ?", before.UTC()).Delete(&models.TaskResult{}).Error
}
