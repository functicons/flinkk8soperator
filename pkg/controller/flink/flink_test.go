package flink

import (
	"context"
	"testing"

	"time"

	"github.com/lyft/flinkk8soperator/pkg/apis/app/v1alpha1"
	"github.com/lyft/flinkk8soperator/pkg/controller/common"
	"github.com/lyft/flinkk8soperator/pkg/controller/flink/client"
	clientMock "github.com/lyft/flinkk8soperator/pkg/controller/flink/client/mock"
	"github.com/lyft/flinkk8soperator/pkg/controller/flink/mock"
	k8mock "github.com/lyft/flinkk8soperator/pkg/controller/k8/mock"
	mockScope "github.com/lyft/flytestdlib/promutils"
	"github.com/lyft/flytestdlib/promutils/labeled"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/apps/v1"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/runtime"
)

const testImage = "123.xyz.com/xx:11ae1218924428faabd9b64423fa0c332efba6b2"

// Note: if you find yourself changing this to fix a test, that should be treated as a breaking API change
const testAppHash = "718222d3"
const testAppName = "app-name"
const testNamespace = "ns"
const testJobID = "j1"
const testFlinkVersion = "1.7"

func getTestFlinkController() Controller {
	testScope := mockScope.NewTestScope()
	labeled.SetMetricKeys(common.GetValidLabelNames()...)
	return Controller{
		jobManager:  &mock.JobManagerController{},
		taskManager: &mock.TaskManagerController{},
		k8Cluster:   &k8mock.K8Cluster{},
		flinkClient: &clientMock.JobManagerClient{},
		metrics:     newControllerMetrics(testScope),
	}
}

func getFlinkTestApp() v1alpha1.FlinkApplication {
	app := v1alpha1.FlinkApplication{
		TypeMeta: metaV1.TypeMeta{
			Kind:       v1alpha1.FlinkApplicationKind,
			APIVersion: v1alpha1.SchemeGroupVersion.String(),
		},
	}
	app.Spec.Parallelism = 8
	app.Name = testAppName
	app.Namespace = testNamespace
	app.Status.JobStatus.JobID = testJobID
	app.Spec.Image = testImage
	app.Spec.FlinkVersion = testFlinkVersion

	return app
}

func TestFlinkIsClusterReady(t *testing.T) {
	flinkControllerForTest := getTestFlinkController()
	labelMapVal := map[string]string{
		"flink-app-hash": testAppHash,
	}
	flinkApp := getFlinkTestApp()

	mockK8Cluster := flinkControllerForTest.k8Cluster.(*k8mock.K8Cluster)
	mockK8Cluster.GetDeploymentsWithLabelFunc = func(ctx context.Context, namespace string, labelMap map[string]string) (*v1.DeploymentList, error) {
		assert.Equal(t, testNamespace, namespace)
		assert.Equal(t, labelMapVal, labelMap)
		jmDeployment := FetchTaskMangerDeploymentCreateObj(&flinkApp, testAppHash)
		jmDeployment.Status.AvailableReplicas = 1

		tmDeployment := FetchJobMangerDeploymentCreateObj(&flinkApp, testAppHash)
		tmDeployment.Status.AvailableReplicas = *tmDeployment.Spec.Replicas
		return &v1.DeploymentList{
			Items: []v1.Deployment{
				*jmDeployment,
				*tmDeployment,
			},
		}, nil
	}

	result, err := flinkControllerForTest.IsClusterReady(
		context.Background(), &flinkApp,
	)
	assert.True(t, result)
	assert.Nil(t, err)
}

func TestFlinkApplicationChangedReplicas(t *testing.T) {
	flinkControllerForTest := getTestFlinkController()
	labelMapVal := map[string]string{
		"flink-app": testAppName,
	}

	flinkApp := getFlinkTestApp()
	taskSlots := int32(16)
	flinkApp.Spec.TaskManagerConfig.TaskSlots = &taskSlots
	flinkApp.Spec.Parallelism = 8

	mockK8Cluster := flinkControllerForTest.k8Cluster.(*k8mock.K8Cluster)
	mockK8Cluster.GetDeploymentsWithLabelFunc = func(ctx context.Context, namespace string, labelMap map[string]string) (*v1.DeploymentList, error) {
		assert.Equal(t, testNamespace, namespace)
		assert.Equal(t, labelMapVal, labelMap)

		newApp := flinkApp.DeepCopy()
		newApp.Spec.Parallelism = 10
		hash := HashForApplication(newApp)
		tm := *FetchTaskMangerDeploymentCreateObj(newApp, hash)
		jm := *FetchJobMangerDeploymentCreateObj(newApp, hash)

		return &v1.DeploymentList{
			Items: []v1.Deployment{tm, jm},
		}, nil
	}

	cur, _, err := flinkControllerForTest.GetCurrentAndOldDeploymentsForApp(
		context.Background(), &flinkApp,
	)
	assert.True(t, cur == nil)
	assert.Nil(t, err)
}

func TestFlinkApplicationNotChanged(t *testing.T) {
	flinkControllerForTest := getTestFlinkController()
	labelMapVal := map[string]string{
		"flink-app": testAppName,
	}
	flinkApp := getFlinkTestApp()
	mockK8Cluster := flinkControllerForTest.k8Cluster.(*k8mock.K8Cluster)
	mockK8Cluster.GetDeploymentsWithLabelFunc = func(ctx context.Context, namespace string, labelMap map[string]string) (*v1.DeploymentList, error) {
		assert.Equal(t, testNamespace, namespace)
		assert.Equal(t, labelMapVal, labelMap)
		return &v1.DeploymentList{
			Items: []v1.Deployment{
				*FetchTaskMangerDeploymentCreateObj(&flinkApp, testAppHash),
				*FetchJobMangerDeploymentCreateObj(&flinkApp, testAppHash),
			},
		}, nil
	}
	cur, _, err := flinkControllerForTest.GetCurrentAndOldDeploymentsForApp(
		context.Background(), &flinkApp,
	)
	assert.Nil(t, err)
	assert.False(t, cur == nil)
}

func TestFlinkApplicationChanged(t *testing.T) {
	flinkControllerForTest := getTestFlinkController()
	labelMapVal := map[string]string{
		"flink-app": testAppName,
	}
	mockK8Cluster := flinkControllerForTest.k8Cluster.(*k8mock.K8Cluster)
	mockK8Cluster.GetDeploymentsWithLabelFunc = func(ctx context.Context, namespace string, labelMap map[string]string) (*v1.DeploymentList, error) {
		assert.Equal(t, testNamespace, namespace)
		assert.Equal(t, labelMapVal, labelMap)
		return &v1.DeploymentList{}, nil
	}
	flinkApp := getFlinkTestApp()
	cur, _, err := flinkControllerForTest.GetCurrentAndOldDeploymentsForApp(
		context.Background(), &flinkApp,
	)
	assert.True(t, cur == nil)
	assert.Nil(t, err)
}

func testJobPropTriggersChange(t *testing.T, changeFun func(application *v1alpha1.FlinkApplication)) {
	flinkControllerForTest := getTestFlinkController()
	flinkApp := getFlinkTestApp()

	mockK8Cluster := flinkControllerForTest.k8Cluster.(*k8mock.K8Cluster)
	mockK8Cluster.GetDeploymentsWithLabelFunc = func(ctx context.Context, namespace string, labelMap map[string]string) (*v1.DeploymentList, error) {
		assert.Equal(t, testNamespace, namespace)
		if val, ok := labelMap["flink-app-hash"]; ok {
			assert.Equal(t, testAppHash, val)
		}
		if val, ok := labelMap["flink-app"]; ok {
			assert.Equal(t, testAppName, val)
		}
		hash := HashForApplication(&flinkApp)
		tm := FetchTaskMangerDeploymentCreateObj(&flinkApp, hash)
		jm := FetchJobMangerDeploymentCreateObj(&flinkApp, hash)
		return &v1.DeploymentList{
			Items: []v1.Deployment{
				*tm, *jm,
			},
		}, nil
	}

	newApp := flinkApp.DeepCopy()
	changeFun(newApp)
	cur, _, err := flinkControllerForTest.GetCurrentAndOldDeploymentsForApp(
		context.Background(), newApp,
	)
	assert.True(t, cur == nil)
	assert.Nil(t, err)
}

func TestFlinkApplicationChangedJobProps(t *testing.T) {
	testJobPropTriggersChange(t, func(app *v1alpha1.FlinkApplication) {
		app.Spec.Parallelism = 3
	})

	testJobPropTriggersChange(t, func(app *v1alpha1.FlinkApplication) {
		app.Spec.JarName = "another.jar"
	})

	testJobPropTriggersChange(t, func(app *v1alpha1.FlinkApplication) {
		app.Spec.ProgramArgs = "--test-change"
	})

	testJobPropTriggersChange(t, func(app *v1alpha1.FlinkApplication) {
		app.Spec.EntryClass = "com.another.Class"
	})
}

func TestFlinkApplicationNeedsUpdate(t *testing.T) {
	flinkControllerForTest := getTestFlinkController()
	flinkApp := getFlinkTestApp()

	mockK8Cluster := flinkControllerForTest.k8Cluster.(*k8mock.K8Cluster)
	mockK8Cluster.GetDeploymentsWithLabelFunc = func(ctx context.Context, namespace string, labelMap map[string]string) (*v1.DeploymentList, error) {
		assert.Equal(t, testNamespace, namespace)
		if val, ok := labelMap["flink-app-hash"]; ok {
			assert.Equal(t, testAppHash, val)
		}
		if val, ok := labelMap["flink-app"]; ok {
			assert.Equal(t, testAppName, val)
		}

		app := getFlinkTestApp()
		jm := FetchJobMangerDeploymentCreateObj(&app, testAppHash)
		tm := FetchTaskMangerDeploymentCreateObj(&app, testAppHash)

		return &v1.DeploymentList{
			Items: []v1.Deployment{
				*jm, *tm,
			},
		}, nil
	}

	numberOfTaskManagers := int32(2)
	taskSlots := int32(2)
	flinkApp.Spec.TaskManagerConfig.TaskSlots = &taskSlots
	flinkApp.Spec.Parallelism = taskSlots*numberOfTaskManagers + 1
	cur, _, err := flinkControllerForTest.GetCurrentAndOldDeploymentsForApp(
		context.Background(), &flinkApp,
	)
	assert.True(t, cur == nil)
	assert.Nil(t, err)
}

func TestFlinkIsServiceReady(t *testing.T) {
	flinkControllerForTest := getTestFlinkController()
	flinkApp := getFlinkTestApp()

	mockJmClient := flinkControllerForTest.flinkClient.(*clientMock.JobManagerClient)
	mockJmClient.GetClusterOverviewFunc = func(ctx context.Context, url string) (*client.ClusterOverviewResponse, error) {
		assert.Equal(t, url, "http://app-name-hash.ns:8081")
		return &client.ClusterOverviewResponse{
			TaskManagerCount: 3,
		}, nil
	}
	isReady, err := flinkControllerForTest.IsServiceReady(context.Background(), &flinkApp, "hash")
	assert.Nil(t, err)
	assert.True(t, isReady)
}

func TestFlinkIsServiceReadyErr(t *testing.T) {
	flinkControllerForTest := getTestFlinkController()
	flinkApp := getFlinkTestApp()

	mockJmClient := flinkControllerForTest.flinkClient.(*clientMock.JobManagerClient)
	mockJmClient.GetClusterOverviewFunc = func(ctx context.Context, url string) (*client.ClusterOverviewResponse, error) {
		assert.Equal(t, url, "http://app-name-hash.ns:8081")
		return nil, errors.New("Get cluster failed")
	}
	isReady, err := flinkControllerForTest.IsServiceReady(context.Background(), &flinkApp, "hash")
	assert.EqualError(t, err, "Get cluster failed")
	assert.False(t, isReady)
}

func TestFlinkGetSavepointStatus(t *testing.T) {
	flinkControllerForTest := getTestFlinkController()
	flinkApp := getFlinkTestApp()
	flinkApp.Spec.SavepointInfo.TriggerID = "t1"

	mockJmClient := flinkControllerForTest.flinkClient.(*clientMock.JobManagerClient)
	mockJmClient.CheckSavepointStatusFunc = func(ctx context.Context, url string, jobID, triggerID string) (*client.SavepointResponse, error) {
		assert.Equal(t, url, "http://app-name-hash.ns:8081")
		assert.Equal(t, jobID, testJobID)
		assert.Equal(t, triggerID, "t1")
		return &client.SavepointResponse{
			SavepointStatus: client.SavepointStatusResponse{
				Status: client.SavePointInProgress,
			},
		}, nil
	}
	status, err := flinkControllerForTest.GetSavepointStatus(context.Background(), &flinkApp, "hash")
	assert.Nil(t, err)
	assert.NotNil(t, status)

	assert.Equal(t, client.SavePointInProgress, status.SavepointStatus.Status)
}

func TestFlinkGetSavepointStatusErr(t *testing.T) {
	flinkControllerForTest := getTestFlinkController()
	flinkApp := getFlinkTestApp()

	mockJmClient := flinkControllerForTest.flinkClient.(*clientMock.JobManagerClient)
	mockJmClient.CheckSavepointStatusFunc = func(ctx context.Context, url string, jobID, triggerID string) (*client.SavepointResponse, error) {
		assert.Equal(t, url, "http://app-name-hash.ns:8081")
		assert.Equal(t, jobID, testJobID)
		return nil, errors.New("Savepoint error")
	}
	status, err := flinkControllerForTest.GetSavepointStatus(context.Background(), &flinkApp, "hash")
	assert.Nil(t, status)
	assert.NotNil(t, err)

	assert.EqualError(t, err, "Savepoint error")
}

func TestGetActiveJob(t *testing.T) {
	job := client.FlinkJob{
		Status: client.Running,
		JobID:  "j1",
	}
	jobs := []client.FlinkJob{
		job,
	}
	activeJob := GetActiveFlinkJob(jobs)
	assert.NotNil(t, activeJob)
	assert.Equal(t, *activeJob, job)
}

func TestGetActiveJobFinished(t *testing.T) {
	job := client.FlinkJob{
		Status: client.Finished,
		JobID:  "j1",
	}
	jobs := []client.FlinkJob{
		job,
	}
	activeJob := GetActiveFlinkJob(jobs)
	assert.NotNil(t, activeJob)
	assert.Equal(t, *activeJob, job)
}

func TestGetActiveJobNil(t *testing.T) {
	job := client.FlinkJob{
		Status: client.Cancelling,
		JobID:  "j1",
	}
	jobs := []client.FlinkJob{
		job,
	}
	activeJob := GetActiveFlinkJob(jobs)
	assert.Nil(t, activeJob)
}

func TestGetActiveJobEmpty(t *testing.T) {
	jobs := []client.FlinkJob{}
	activeJob := GetActiveFlinkJob(jobs)
	assert.Nil(t, activeJob)
}

func TestDeleteCluster(t *testing.T) {
	flinkControllerForTest := getTestFlinkController()
	flinkApp := getFlinkTestApp()
	jmDeployment := FetchJobMangerDeploymentDeleteObj(&flinkApp, "hash")
	tmDeployment := FetchTaskMangerDeploymentDeleteObj(&flinkApp, "hash")
	service := FetchVersionedJobManagerServiceDeleteObj(&flinkApp, "hash")

	ctr := 0
	mockK8Cluster := flinkControllerForTest.k8Cluster.(*k8mock.K8Cluster)
	mockK8Cluster.DeleteK8ObjectFunc = func(ctx context.Context, object runtime.Object) error {
		ctr++
		switch ctr {
		case 1:
			assert.Equal(t, object, jmDeployment)
		case 2:
			assert.Equal(t, object, tmDeployment)
		case 3:
			assert.Equal(t, object, service)
		}
		return nil
	}

	err := flinkControllerForTest.DeleteCluster(context.Background(), &flinkApp, "hash")
	assert.Nil(t, err)
}

func TestCreateCluster(t *testing.T) {
	flinkControllerForTest := getTestFlinkController()
	flinkApp := getFlinkTestApp()
	mockJobManager := flinkControllerForTest.jobManager.(*mock.JobManagerController)
	mockTaskManager := flinkControllerForTest.taskManager.(*mock.TaskManagerController)

	mockJobManager.CreateIfNotExistFunc = func(ctx context.Context, application *v1alpha1.FlinkApplication) (bool, error) {
		return true, nil
	}
	mockTaskManager.CreateIfNotExistFunc = func(ctx context.Context, application *v1alpha1.FlinkApplication) (bool, error) {
		return true, nil
	}
	err := flinkControllerForTest.CreateCluster(context.Background(), &flinkApp)
	assert.Nil(t, err)
}

func TestCreateClusterJmErr(t *testing.T) {
	flinkControllerForTest := getTestFlinkController()
	flinkApp := getFlinkTestApp()
	mockJobManager := flinkControllerForTest.jobManager.(*mock.JobManagerController)
	mockTaskManager := flinkControllerForTest.taskManager.(*mock.TaskManagerController)

	mockJobManager.CreateIfNotExistFunc = func(ctx context.Context, application *v1alpha1.FlinkApplication) (bool, error) {
		return false, errors.New("jm failed")
	}
	mockTaskManager.CreateIfNotExistFunc = func(ctx context.Context, application *v1alpha1.FlinkApplication) (bool, error) {
		assert.False(t, true)
		return false, nil
	}
	err := flinkControllerForTest.CreateCluster(context.Background(), &flinkApp)
	assert.EqualError(t, err, "jm failed")
}

func TestCreateClusterTmErr(t *testing.T) {
	flinkControllerForTest := getTestFlinkController()
	flinkApp := getFlinkTestApp()
	mockJobManager := flinkControllerForTest.jobManager.(*mock.JobManagerController)
	mockTaskManager := flinkControllerForTest.taskManager.(*mock.TaskManagerController)

	mockJobManager.CreateIfNotExistFunc = func(ctx context.Context, application *v1alpha1.FlinkApplication) (bool, error) {
		return true, nil
	}
	mockTaskManager.CreateIfNotExistFunc = func(ctx context.Context, application *v1alpha1.FlinkApplication) (bool, error) {
		return false, errors.New("tm failed")
	}
	err := flinkControllerForTest.CreateCluster(context.Background(), &flinkApp)
	assert.EqualError(t, err, "tm failed")
}

func TestStartFlinkJob(t *testing.T) {
	flinkControllerForTest := getTestFlinkController()
	flinkApp := getFlinkTestApp()
	flinkApp.Spec.Parallelism = 4
	flinkApp.Spec.ProgramArgs = "args"
	flinkApp.Spec.EntryClass = "class"
	flinkApp.Spec.JarName = "jar-name"
	flinkApp.Spec.SavepointInfo.SavepointLocation = "location//"
	flinkApp.Spec.FlinkVersion = "1.7"

	mockJmClient := flinkControllerForTest.flinkClient.(*clientMock.JobManagerClient)
	mockJmClient.SubmitJobFunc = func(ctx context.Context, url string, jarID string, submitJobRequest client.SubmitJobRequest) (*client.SubmitJobResponse, error) {
		assert.Equal(t, url, "http://app-name-hash.ns:8081")
		assert.Equal(t, jarID, "jar-name")
		assert.Equal(t, submitJobRequest.Parallelism, int32(4))
		assert.Equal(t, submitJobRequest.ProgramArgs, "args")
		assert.Equal(t, submitJobRequest.EntryClass, "class")
		assert.Equal(t, submitJobRequest.SavepointPath, "location//")

		return &client.SubmitJobResponse{
			JobID: testJobID,
		}, nil
	}
	jobID, err := flinkControllerForTest.StartFlinkJob(context.Background(), &flinkApp, "hash",
		flinkApp.Spec.JarName, flinkApp.Spec.Parallelism, flinkApp.Spec.EntryClass, flinkApp.Spec.ProgramArgs)
	assert.Nil(t, err)
	assert.Equal(t, jobID, testJobID)
}

func TestStartFlinkJobEmptyJobID(t *testing.T) {
	flinkControllerForTest := getTestFlinkController()
	flinkApp := getFlinkTestApp()

	mockJmClient := flinkControllerForTest.flinkClient.(*clientMock.JobManagerClient)
	mockJmClient.SubmitJobFunc = func(ctx context.Context, url string, jarID string, submitJobRequest client.SubmitJobRequest) (*client.SubmitJobResponse, error) {

		return &client.SubmitJobResponse{}, nil
	}
	jobID, err := flinkControllerForTest.StartFlinkJob(context.Background(), &flinkApp, "hash",
		flinkApp.Spec.JarName, flinkApp.Spec.Parallelism, flinkApp.Spec.EntryClass, flinkApp.Spec.ProgramArgs)
	assert.EqualError(t, err, "unable to submit job: invalid job id")
	assert.Empty(t, jobID)
}

func TestStartFlinkJobErr(t *testing.T) {
	flinkControllerForTest := getTestFlinkController()
	flinkApp := getFlinkTestApp()

	mockJmClient := flinkControllerForTest.flinkClient.(*clientMock.JobManagerClient)
	mockJmClient.SubmitJobFunc = func(ctx context.Context, url string, jarID string, submitJobRequest client.SubmitJobRequest) (*client.SubmitJobResponse, error) {
		return nil, errors.New("submit error")
	}
	jobID, err := flinkControllerForTest.StartFlinkJob(context.Background(), &flinkApp, "hash",
		flinkApp.Spec.JarName, flinkApp.Spec.Parallelism, flinkApp.Spec.EntryClass, flinkApp.Spec.ProgramArgs)
	assert.EqualError(t, err, "submit error")
	assert.Empty(t, jobID)
}

func TestCancelWithSavepoint(t *testing.T) {
	flinkControllerForTest := getTestFlinkController()
	flinkApp := getFlinkTestApp()

	mockJmClient := flinkControllerForTest.flinkClient.(*clientMock.JobManagerClient)
	mockJmClient.CancelJobWithSavepointFunc = func(ctx context.Context, url string, jobID string) (string, error) {
		assert.Equal(t, url, "http://app-name-hash.ns:8081")
		assert.Equal(t, jobID, testJobID)
		return "t1", nil
	}
	triggerID, err := flinkControllerForTest.CancelWithSavepoint(context.Background(), &flinkApp, "hash")
	assert.Nil(t, err)
	assert.Equal(t, triggerID, "t1")
}

func TestCancelWithSavepointErr(t *testing.T) {
	flinkControllerForTest := getTestFlinkController()
	flinkApp := getFlinkTestApp()

	mockJmClient := flinkControllerForTest.flinkClient.(*clientMock.JobManagerClient)
	mockJmClient.CancelJobWithSavepointFunc = func(ctx context.Context, url string, jobID string) (string, error) {
		return "", errors.New("cancel error")
	}
	triggerID, err := flinkControllerForTest.CancelWithSavepoint(context.Background(), &flinkApp, "hash")
	assert.EqualError(t, err, "cancel error")
	assert.Empty(t, triggerID)
}

func TestGetJobsForApplication(t *testing.T) {
	flinkControllerForTest := getTestFlinkController()
	flinkApp := getFlinkTestApp()

	mockJmClient := flinkControllerForTest.flinkClient.(*clientMock.JobManagerClient)
	mockJmClient.GetJobsFunc = func(ctx context.Context, url string) (*client.GetJobsResponse, error) {
		assert.Equal(t, url, "http://app-name-hash.ns:8081")
		return &client.GetJobsResponse{
			Jobs: []client.FlinkJob{
				{
					JobID: testJobID,
				},
			},
		}, nil
	}
	jobs, err := flinkControllerForTest.GetJobsForApplication(context.Background(), &flinkApp, "hash")
	assert.Nil(t, err)
	assert.Equal(t, 1, len(jobs))
	assert.Equal(t, jobs[0].JobID, testJobID)
}

func TestGetJobsForApplicationErr(t *testing.T) {
	flinkControllerForTest := getTestFlinkController()
	flinkApp := getFlinkTestApp()

	mockJmClient := flinkControllerForTest.flinkClient.(*clientMock.JobManagerClient)
	mockJmClient.GetJobsFunc = func(ctx context.Context, url string) (*client.GetJobsResponse, error) {
		return nil, errors.New("get jobs error")
	}
	jobs, err := flinkControllerForTest.GetJobsForApplication(context.Background(), &flinkApp, "hash")
	assert.EqualError(t, err, "get jobs error")
	assert.Nil(t, jobs)
}

func TestFindExternalizedCheckpoint(t *testing.T) {
	flinkControllerForTest := getTestFlinkController()
	flinkApp := getFlinkTestApp()
	flinkApp.Status.JobStatus.JobID = "jobid"

	mockJmClient := flinkControllerForTest.flinkClient.(*clientMock.JobManagerClient)
	mockJmClient.GetLatestCheckpointFunc = func(ctx context.Context, url string, jobId string) (*client.CheckpointStatistics, error) {
		assert.Equal(t, url, "http://app-name-hash.ns:8081")
		assert.Equal(t, "jobid", jobId)
		return &client.CheckpointStatistics{
			TriggerTimestamp: time.Now().Unix(),
			ExternalPath:     "/tmp/checkpoint",
		}, nil
	}

	checkpoint, err := flinkControllerForTest.FindExternalizedCheckpoint(context.Background(), &flinkApp, "hash")
	assert.Nil(t, err)
	assert.Equal(t, "/tmp/checkpoint", checkpoint)
}

func TestClusterStatusUpdated(t *testing.T) {
	flinkControllerForTest := getTestFlinkController()
	flinkApp := getFlinkTestApp()

	mockJmClient := flinkControllerForTest.flinkClient.(*clientMock.JobManagerClient)
	mockJmClient.GetClusterOverviewFunc = func(ctx context.Context, url string) (*client.ClusterOverviewResponse, error) {
		assert.Equal(t, url, "http://app-name-hash.ns:8081")
		return &client.ClusterOverviewResponse{
			NumberOfTaskSlots: 1,
			SlotsAvailable:    0,
			TaskManagerCount:  1,
		}, nil
	}

	mockJmClient.GetTaskManagersFunc = func(ctx context.Context, url string) (*client.TaskManagersResponse, error) {
		assert.Equal(t, url, "http://app-name-hash.ns:8081")
		return &client.TaskManagersResponse{
			TaskManagers: []client.TaskManagerStats{
				{
					TimeSinceLastHeartbeat: time.Now().UnixNano() / int64(time.Millisecond),
					SlotsNumber:            3,
					FreeSlots:              0,
				},
			},
		}, nil
	}

	_, err := flinkControllerForTest.CompareAndUpdateClusterStatus(context.Background(), &flinkApp, "hash")
	assert.Nil(t, err)
	assert.Equal(t, int32(1), flinkApp.Status.ClusterStatus.NumberOfTaskSlots)
	assert.Equal(t, int32(0), flinkApp.Status.ClusterStatus.AvailableTaskSlots)
	assert.Equal(t, int32(1), flinkApp.Status.ClusterStatus.HealthyTaskManagers)
	assert.Equal(t, v1alpha1.Green, flinkApp.Status.ClusterStatus.Health)

}

func TestNoClusterStatusChange(t *testing.T) {
	flinkControllerForTest := getTestFlinkController()
	flinkApp := getFlinkTestApp()
	flinkApp.Status.ClusterStatus.NumberOfTaskSlots = int32(1)
	flinkApp.Status.ClusterStatus.AvailableTaskSlots = int32(0)
	flinkApp.Status.ClusterStatus.HealthyTaskManagers = int32(1)
	flinkApp.Status.ClusterStatus.Health = v1alpha1.Green
	flinkApp.Status.ClusterStatus.NumberOfTaskManagers = int32(1)
	mockJmClient := flinkControllerForTest.flinkClient.(*clientMock.JobManagerClient)
	mockJmClient.GetClusterOverviewFunc = func(ctx context.Context, url string) (*client.ClusterOverviewResponse, error) {
		assert.Equal(t, url, "http://app-name-hash.ns:8081")
		return &client.ClusterOverviewResponse{
			NumberOfTaskSlots: 1,
			SlotsAvailable:    0,
			TaskManagerCount:  1,
		}, nil
	}

	mockJmClient.GetTaskManagersFunc = func(ctx context.Context, url string) (*client.TaskManagersResponse, error) {
		assert.Equal(t, url, "http://app-name-hash.ns:8081")
		return &client.TaskManagersResponse{
			TaskManagers: []client.TaskManagerStats{
				{
					TimeSinceLastHeartbeat: time.Now().UnixNano() / int64(time.Millisecond),
					SlotsNumber:            3,
					FreeSlots:              0,
				},
			},
		}, nil
	}

	hasClusterStatusChanged, err := flinkControllerForTest.CompareAndUpdateClusterStatus(context.Background(), &flinkApp, "hash")
	assert.Nil(t, err)
	assert.False(t, hasClusterStatusChanged)
}

func TestHealthyTaskmanagers(t *testing.T) {
	flinkControllerForTest := getTestFlinkController()
	flinkApp := getFlinkTestApp()

	mockJmClient := flinkControllerForTest.flinkClient.(*clientMock.JobManagerClient)

	mockJmClient.GetClusterOverviewFunc = func(ctx context.Context, url string) (*client.ClusterOverviewResponse, error) {
		assert.Equal(t, url, "http://app-name-hash.ns:8081")
		return &client.ClusterOverviewResponse{
			NumberOfTaskSlots: 1,
			SlotsAvailable:    0,
			TaskManagerCount:  1,
		}, nil
	}

	mockJmClient.GetTaskManagersFunc = func(ctx context.Context, url string) (*client.TaskManagersResponse, error) {
		assert.Equal(t, url, "http://app-name-hash.ns:8081")
		return &client.TaskManagersResponse{
			TaskManagers: []client.TaskManagerStats{
				{
					// 1 day old
					TimeSinceLastHeartbeat: time.Now().AddDate(0, 0, -1).UnixNano() / int64(time.Millisecond),
					SlotsNumber:            3,
					FreeSlots:              0,
				},
			},
		}, nil
	}

	_, err := flinkControllerForTest.CompareAndUpdateClusterStatus(context.Background(), &flinkApp, "hash")
	assert.Nil(t, err)
	assert.Equal(t, int32(1), flinkApp.Status.ClusterStatus.NumberOfTaskSlots)
	assert.Equal(t, int32(0), flinkApp.Status.ClusterStatus.AvailableTaskSlots)
	assert.Equal(t, int32(0), flinkApp.Status.ClusterStatus.HealthyTaskManagers)
	assert.Equal(t, v1alpha1.Yellow, flinkApp.Status.ClusterStatus.Health)

}

func TestJobStatusUpdated(t *testing.T) {
	flinkControllerForTest := getTestFlinkController()
	flinkApp := getFlinkTestApp()
	startTime := metaV1.Now().UnixNano() / int64(time.Millisecond)
	mockJmClient := flinkControllerForTest.flinkClient.(*clientMock.JobManagerClient)
	mockJmClient.GetJobOverviewFunc = func(ctx context.Context, url string, jobID string) (*client.FlinkJobOverview, error) {
		assert.Equal(t, url, "http://app-name-hash.ns:8081")
		return &client.FlinkJobOverview{
			JobID:     "abc",
			State:     client.Running,
			StartTime: startTime,
		}, nil
	}

	mockJmClient.GetCheckpointCountsFunc = func(ctx context.Context, url string, jobID string) (*client.CheckpointResponse, error) {
		assert.Equal(t, url, "http://app-name-hash.ns:8081")
		return &client.CheckpointResponse{
			Counts: map[string]int32{
				"restored":  1,
				"completed": 4,
				"failed":    0,
			},
			Latest: client.LatestCheckpoints{
				Restored: &client.CheckpointStatistics{
					RestoredTimeStamp: startTime,
					ExternalPath:      "/test/externalpath",
				},

				Completed: &client.CheckpointStatistics{
					LatestAckTimestamp: startTime,
				},
			},
		}, nil
	}

	flinkApp.Status.JobStatus.JobID = "abc"
	expectedTime := metaV1.NewTime(time.Unix(startTime/1000, 0))
	_, err := flinkControllerForTest.CompareAndUpdateJobStatus(context.Background(), &flinkApp, "hash")
	assert.Nil(t, err)

	assert.Equal(t, v1alpha1.Running, flinkApp.Status.JobStatus.State)
	assert.Equal(t, &expectedTime, flinkApp.Status.JobStatus.StartTime)
	assert.Equal(t, v1alpha1.Green, flinkApp.Status.JobStatus.Health)

	assert.Equal(t, int32(0), flinkApp.Status.JobStatus.FailedCheckpointCount)
	assert.Equal(t, int32(4), flinkApp.Status.JobStatus.CompletedCheckpointCount)
	assert.Equal(t, int32(1), flinkApp.Status.JobStatus.JobRestartCount)
	assert.Equal(t, &expectedTime, flinkApp.Status.JobStatus.RestoreTime)
	assert.Equal(t, "/test/externalpath", flinkApp.Status.JobStatus.RestorePath)
	assert.Equal(t, &expectedTime, flinkApp.Status.JobStatus.LastCheckpointTime)

}

func TestNoJobStatusChange(t *testing.T) {
	flinkControllerForTest := getTestFlinkController()
	constTime := time.Now().UnixNano() / int64(time.Millisecond)
	metaTime := metaV1.NewTime(time.Unix(constTime/1000, 0))
	app1 := getFlinkTestApp()
	mockJmClient := flinkControllerForTest.flinkClient.(*clientMock.JobManagerClient)

	app1.Status.JobStatus.State = v1alpha1.Running
	app1.Status.JobStatus.StartTime = &metaTime
	app1.Status.JobStatus.LastCheckpointTime = &metaTime
	app1.Status.JobStatus.CompletedCheckpointCount = int32(4)
	app1.Status.JobStatus.JobRestartCount = int32(1)
	app1.Status.JobStatus.FailedCheckpointCount = int32(0)
	app1.Status.JobStatus.Health = v1alpha1.Green
	app1.Status.JobStatus.RestoreTime = &metaTime
	app1.Status.JobStatus.RestorePath = "/test/externalpath"

	mockJmClient.GetJobOverviewFunc = func(ctx context.Context, url string, jobID string) (*client.FlinkJobOverview, error) {
		assert.Equal(t, url, "http://app-name-hash.ns:8081")
		return &client.FlinkJobOverview{
			JobID:     "j1",
			State:     client.Running,
			StartTime: constTime,
		}, nil
	}

	mockJmClient.GetCheckpointCountsFunc = func(ctx context.Context, url string, jobID string) (*client.CheckpointResponse, error) {
		assert.Equal(t, url, "http://app-name-hash.ns:8081")
		return &client.CheckpointResponse{
			Counts: map[string]int32{
				"restored":  1,
				"completed": 4,
				"failed":    0,
			},
			Latest: client.LatestCheckpoints{
				Restored: &client.CheckpointStatistics{
					RestoredTimeStamp: constTime,
					ExternalPath:      "/test/externalpath",
				},

				Completed: &client.CheckpointStatistics{
					LatestAckTimestamp: constTime,
				},
			},
		}, nil
	}
	hasJobStatusChanged, err := flinkControllerForTest.CompareAndUpdateJobStatus(context.Background(), &app1, "hash")
	assert.Nil(t, err)
	assert.False(t, hasJobStatusChanged)

}

func TestGetAndUpdateJobStatusHealth(t *testing.T) {
	flinkControllerForTest := getTestFlinkController()
	lastFailedTime := metaV1.NewTime(time.Now().Add(-10 * time.Second))
	app1 := getFlinkTestApp()
	mockJmClient := flinkControllerForTest.flinkClient.(*clientMock.JobManagerClient)

	app1.Status.JobStatus.State = v1alpha1.Failing
	app1.Status.JobStatus.LastFailingTime = &lastFailedTime

	mockJmClient.GetJobOverviewFunc = func(ctx context.Context, url string, jobID string) (*client.FlinkJobOverview, error) {
		assert.Equal(t, url, "http://app-name-hash.ns:8081")
		return &client.FlinkJobOverview{
			JobID:     "abc",
			State:     client.Running,
			StartTime: metaV1.Now().UnixNano() / int64(time.Millisecond),
		}, nil
	}

	mockJmClient.GetCheckpointCountsFunc = func(ctx context.Context, url string, jobID string) (*client.CheckpointResponse, error) {
		assert.Equal(t, url, "http://app-name-hash.ns:8081")
		return &client.CheckpointResponse{
			Counts: map[string]int32{
				"restored":  1,
				"completed": 4,
				"failed":    0,
			},
		}, nil
	}
	_, err := flinkControllerForTest.CompareAndUpdateJobStatus(context.Background(), &app1, "hash")
	assert.Nil(t, err)
	// Job is in a RUNNING state but was in a FAILING state in the last 1 minute, so we expect
	// JobStatus.Health to be Red
	assert.Equal(t, app1.Status.JobStatus.Health, v1alpha1.Red)

}
