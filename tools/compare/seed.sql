-- Test data for tools/compare/run.sh, applied over internal/db/dbtest/schema.sql.
INSERT INTO TMSettings VALUES ('s','tok123',1,NULL);
INSERT INTO TMArea VALUES ('AREA1','Work',1,1);
INSERT INTO TMTag VALUES ('TAG1','urgent',NULL,NULL,1);
-- project open, project done
INSERT INTO TMTask (uuid,title,notes,type,status,creationDate,userModificationDate,trashed,start,startBucket,"index",area,untrashedLeafActionsCount,openUntrashedLeafActionsCount) VALUES
 ('PROJ1','Alpha project','',1,0,1760000000,1760000000,0,1,0,1,'AREA1',2,1),
 ('PROJ2','Beta project','',1,3,1760000000,1760000000,0,1,0,2,NULL,0,0);
INSERT INTO TMTask (uuid,title,notes,type,status,creationDate,userModificationDate,trashed,start,startBucket,"index",project) VALUES
 ('HEAD1','Phase one','',2,0,1760000000,1760000000,0,1,0,1,'PROJ1');
INSERT INTO TMTask (uuid,title,notes,type,status,stopDate,creationDate,userModificationDate,trashed,start,startBucket,startDate,deadline,"index",todayIndex,area,project,heading,rt1_recurrenceRule) VALUES
 ('TODO1','Buy milk','some notes',0,0,NULL,1760000000,1760000000,0,1,0,132818176,NULL,1,1,NULL,NULL,NULL,NULL),
 ('TODO2','Write report','',0,0,NULL,1760000000,1760000000,0,1,0,NULL,132818304,2,0,NULL,'PROJ1','HEAD1',NULL),
 ('TODO3','Done thing','',0,3,1760090000,1760000000,1760000000,0,1,0,NULL,NULL,3,0,NULL,NULL,NULL,NULL),
 ('TODO4','Cancelled thing','',0,2,1760090000,1760000000,1760000000,0,1,0,NULL,NULL,4,0,NULL,NULL,NULL,NULL),
 ('TODO5','Repeat thing','',0,0,NULL,1760000000,1760000000,0,1,0,NULL,NULL,5,0,NULL,NULL,NULL,x'00'),
 ('TODO6','Trashed thing','',0,0,NULL,1760000000,1760000000,1,1,0,NULL,NULL,6,0,NULL,NULL,NULL,NULL),
 ('TODO7','Inbox item','',0,0,NULL,1760000000,1760000000,0,0,0,NULL,NULL,7,0,NULL,NULL,NULL,NULL),
 ('TODO8','Someday item','',0,0,NULL,1760000000,1760000000,0,2,0,NULL,NULL,8,0,'AREA1',NULL,NULL,NULL),
 ('TODO9','Buy milk','dup title',0,0,NULL,1760000000,1760000000,0,1,0,NULL,NULL,9,0,NULL,NULL,NULL,NULL);
INSERT INTO TMTaskTag VALUES ('TODO1','TAG1');
INSERT INTO TMChecklistItem VALUES ('CL1','eggs',0,NULL,1,'TODO1'),('CL2','bread',3,1760090000,2,'TODO1');
